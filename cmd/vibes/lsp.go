package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/mgomes/vibescript/internal/ast"
	vibesruntime "github.com/mgomes/vibescript/internal/runtime"
	"github.com/mgomes/vibescript/vibes"
)

const maxLSPPayloadBytes = 8 << 20

// jsonNull is the explicit JSON null used for intentionally empty
// results. lspOutboundMessage.Result is omitempty so notifications can
// share the struct, which means a plain nil would drop the result field
// entirely and produce a response with neither result nor error —
// invalid JSON-RPC that strict clients reject.
var jsonNull = json.RawMessage("null")

var lspKeywords = ast.Keywords()

var lspBuiltins = []string{
	"assert",
	"money",
	"money_cents",
	"now",
	"random_id",
	"require",
	"to_float",
	"to_int",
	"uuid",
	"Hash",
	"JSON",
	"Regex",
	"Time",
}

type lspInboundMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type lspResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type lspOutboundMessage struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      *json.RawMessage  `json:"id,omitempty"`
	Method  string            `json:"method,omitempty"`
	Params  any               `json:"params,omitempty"`
	Result  any               `json:"result,omitempty"`
	Error   *lspResponseError `json:"error,omitempty"`
}

type lspDidOpenParams struct {
	TextDocument struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"textDocument"`
}

type lspDidChangeParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

type lspDocumentFormattingParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

type lspTextDocumentPositionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
}

type lspServer struct {
	reader *bufio.Reader
	writer *bufio.Writer
	engine *vibes.Engine
	docs   map[string]string
	lines  map[string][]string
	// compiled holds the most recent successfully compiled script per
	// document, so completion can offer user-defined symbols while the
	// buffer is mid-edit and temporarily unparsable.
	compiled map[string]*vibes.Script
	// completions holds the per-document script-local completion index
	// derived from compiled and re-anchored to the current buffer lines.
	// It is built lazily on the first completion request for a document
	// version and invalidated on every edit, so the diagnostics-only
	// publish path (the editor hot path) does not pay for cloning the
	// compiled functions and rescanning the buffer when no completion is
	// requested.
	completions map[string]*lspCompletionIndex
	// programs holds the most recent parse that produced any top-level
	// statements per document. Unlike compiled it tolerates partial
	// parses, so navigation keeps working while the buffer is mid-edit.
	programs map[string]*ast.Program
}

func runLSP() error {
	server := &lspServer{
		reader:      bufio.NewReader(os.Stdin),
		writer:      bufio.NewWriter(os.Stdout),
		engine:      vibes.MustNewEngine(vibes.Config{}),
		docs:        make(map[string]string),
		lines:       make(map[string][]string),
		compiled:    make(map[string]*vibes.Script),
		completions: make(map[string]*lspCompletionIndex),
		programs:    make(map[string]*ast.Program),
	}
	return server.serve()
}

func (s *lspServer) serve() error {
	for {
		payload, err := s.readPayload()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("lsp read: %w", err)
		}

		var incoming lspInboundMessage
		if err := json.Unmarshal(payload, &incoming); err != nil {
			continue
		}

		messages := s.handleMessage(incoming)
		for _, msg := range messages {
			if err := s.writePayload(msg); err != nil {
				return fmt.Errorf("lsp write: %w", err)
			}
		}

		if incoming.Method == "exit" {
			return nil
		}
	}
}

func (s *lspServer) handleMessage(incoming lspInboundMessage) []lspOutboundMessage {
	switch incoming.Method {
	case "initialize":
		return []lspOutboundMessage{
			{
				JSONRPC: "2.0",
				ID:      incoming.ID,
				Result: map[string]any{
					"capabilities": map[string]any{
						"textDocumentSync":           1,
						"hoverProvider":              true,
						"documentFormattingProvider": true,
						"definitionProvider":         true,
						"documentSymbolProvider":     true,
						"signatureHelpProvider": map[string]any{
							"triggerCharacters": []string{"(", ","},
						},
						"completionProvider": map[string]any{
							"resolveProvider":   false,
							"triggerCharacters": []string{"."},
						},
					},
				},
			},
		}
	case "initialized":
		return nil
	case "shutdown":
		if incoming.ID == nil {
			return nil
		}
		return []lspOutboundMessage{{JSONRPC: "2.0", ID: incoming.ID, Result: jsonNull}}
	case "exit":
		return nil
	case "textDocument/didOpen":
		var params lspDidOpenParams
		if err := json.Unmarshal(incoming.Params, &params); err != nil {
			return nil
		}
		s.setDocument(params.TextDocument.URI, params.TextDocument.Text)
		return []lspOutboundMessage{
			s.publishDiagnostics(params.TextDocument.URI, params.TextDocument.Text),
		}
	case "textDocument/didChange":
		var params lspDidChangeParams
		if err := json.Unmarshal(incoming.Params, &params); err != nil {
			return nil
		}
		if len(params.ContentChanges) == 0 {
			return nil
		}
		latest := params.ContentChanges[len(params.ContentChanges)-1].Text
		s.setDocument(params.TextDocument.URI, latest)
		return []lspOutboundMessage{
			s.publishDiagnostics(params.TextDocument.URI, latest),
		}
	case "textDocument/formatting":
		if incoming.ID == nil {
			return nil
		}
		var params lspDocumentFormattingParams
		if err := json.Unmarshal(incoming.Params, &params); err != nil {
			return []lspOutboundMessage{
				{
					JSONRPC: "2.0",
					ID:      incoming.ID,
					Error:   &lspResponseError{Code: -32602, Message: "invalid formatting params"},
				},
			}
		}
		source, ok := s.docs[params.TextDocument.URI]
		if !ok {
			return []lspOutboundMessage{
				{JSONRPC: "2.0", ID: incoming.ID, Result: jsonNull},
			}
		}
		return []lspOutboundMessage{
			{
				JSONRPC: "2.0",
				ID:      incoming.ID,
				Result:  formattingEditsForLines(source, s.documentLines(params.TextDocument.URI)),
			},
		}
	case "textDocument/definition":
		if incoming.ID == nil {
			return nil
		}
		var params lspTextDocumentPositionParams
		if err := json.Unmarshal(incoming.Params, &params); err != nil {
			return []lspOutboundMessage{
				{
					JSONRPC: "2.0",
					ID:      incoming.ID,
					Error:   &lspResponseError{Code: -32602, Message: "invalid definition params"},
				},
			}
		}
		uri := params.TextDocument.URI
		lines := s.documentLines(uri)
		word := wordAtPosition(lines, params.Position.Line, params.Position.Character)
		location := definitionLocation(s.programs[uri], uri, lines, word)
		if location == nil {
			return []lspOutboundMessage{
				{JSONRPC: "2.0", ID: incoming.ID, Result: jsonNull},
			}
		}
		return []lspOutboundMessage{
			{JSONRPC: "2.0", ID: incoming.ID, Result: location},
		}
	case "textDocument/documentSymbol":
		if incoming.ID == nil {
			return nil
		}
		var params lspDocumentFormattingParams
		if err := json.Unmarshal(incoming.Params, &params); err != nil {
			return []lspOutboundMessage{
				{
					JSONRPC: "2.0",
					ID:      incoming.ID,
					Error:   &lspResponseError{Code: -32602, Message: "invalid documentSymbol params"},
				},
			}
		}
		uri := params.TextDocument.URI
		return []lspOutboundMessage{
			{
				JSONRPC: "2.0",
				ID:      incoming.ID,
				Result:  documentSymbols(s.programs[uri], s.documentLines(uri)),
			},
		}
	case "textDocument/signatureHelp":
		if incoming.ID == nil {
			return nil
		}
		var params lspTextDocumentPositionParams
		if err := json.Unmarshal(incoming.Params, &params); err != nil {
			return []lspOutboundMessage{
				{
					JSONRPC: "2.0",
					ID:      incoming.ID,
					Error:   &lspResponseError{Code: -32602, Message: "invalid signatureHelp params"},
				},
			}
		}
		uri := params.TextDocument.URI
		help := s.signatureHelpAt(uri, s.documentLines(uri), params.Position.Line, params.Position.Character)
		if help == nil {
			return []lspOutboundMessage{
				{JSONRPC: "2.0", ID: incoming.ID, Result: jsonNull},
			}
		}
		return []lspOutboundMessage{
			{JSONRPC: "2.0", ID: incoming.ID, Result: help},
		}
	case "textDocument/completion":
		if incoming.ID == nil {
			return nil
		}
		var params lspTextDocumentPositionParams
		if err := json.Unmarshal(incoming.Params, &params); err != nil {
			return []lspOutboundMessage{
				{
					JSONRPC: "2.0",
					ID:      incoming.ID,
					Error:   &lspResponseError{Code: -32602, Message: "invalid completion params"},
				},
			}
		}
		uri := params.TextDocument.URI
		items := s.completionItemsAt(uri, s.documentLines(uri), params.Position.Line, params.Position.Character)
		return []lspOutboundMessage{
			{
				JSONRPC: "2.0",
				ID:      incoming.ID,
				Result: map[string]any{
					"isIncomplete": false,
					"items":        items,
				},
			},
		}
	case "textDocument/hover":
		if incoming.ID == nil {
			return nil
		}
		var params lspTextDocumentPositionParams
		if err := json.Unmarshal(incoming.Params, &params); err != nil {
			return []lspOutboundMessage{
				{
					JSONRPC: "2.0",
					ID:      incoming.ID,
					Error:   &lspResponseError{Code: -32602, Message: "invalid hover params"},
				},
			}
		}
		word := wordAtPosition(s.documentLines(params.TextDocument.URI), params.Position.Line, params.Position.Character)
		if word == "" {
			return []lspOutboundMessage{
				{JSONRPC: "2.0", ID: incoming.ID, Result: jsonNull},
			}
		}
		kind := classifyWord(word)
		return []lspOutboundMessage{
			{
				JSONRPC: "2.0",
				ID:      incoming.ID,
				Result: map[string]any{
					"contents": map[string]any{
						"kind":  "markdown",
						"value": fmt.Sprintf("`%s`\n\nVibescript %s", word, kind),
					},
				},
			},
		}
	default:
		if incoming.ID == nil {
			return nil
		}
		return []lspOutboundMessage{
			{
				JSONRPC: "2.0",
				ID:      incoming.ID,
				Error: &lspResponseError{
					Code:    -32601,
					Message: "method not found",
				},
			},
		}
	}
}

func (s *lspServer) setDocument(uri, source string) {
	if s.docs == nil {
		s.docs = make(map[string]string)
	}
	if s.lines == nil {
		s.lines = make(map[string][]string)
	}
	s.docs[uri] = source
	s.lines[uri] = splitLSPLines(source)
	if s.completions != nil {
		delete(s.completions, uri)
	}
}

func (s *lspServer) documentLines(uri string) []string {
	if s.lines == nil {
		s.lines = make(map[string][]string)
	}
	if lines, ok := s.lines[uri]; ok {
		return lines
	}
	source := ""
	if s.docs != nil {
		source = s.docs[uri]
	}
	lines := splitLSPLines(source)
	s.lines[uri] = lines
	return lines
}

func (s *lspServer) publishDiagnostics(uri, source string) lspOutboundMessage {
	script, program, parseErrs, diagnostics := compileForDiagnostics(s.engine, source)
	// The completion index is derived from the compiled script and the
	// current buffer lines. setDocument already invalidated any cached
	// index for this edit, so the diagnostics path only needs to update
	// the compiled-script cache; the index is rebuilt lazily on the next
	// completion request. This keeps the hot didChange path from cloning
	// every function and rescanning the buffer on each keystroke.
	if script != nil && s.compiled != nil {
		s.compiled[uri] = script
	} else if program == nil && len(parseErrs) == 0 && s.compiled != nil {
		delete(s.compiled, uri)
	}
	if program != nil && s.programs != nil {
		// A clean parse is authoritative even when empty (the symbols
		// are genuinely gone); a broken mid-edit parse only replaces
		// the cache when it still yielded statements.
		if len(parseErrs) == 0 || len(program.Statements) > 0 {
			s.programs[uri] = program
		}
	} else if len(parseErrs) == 0 && s.programs != nil {
		delete(s.programs, uri)
	}
	return lspOutboundMessage{
		JSONRPC: "2.0",
		Method:  "textDocument/publishDiagnostics",
		Params: map[string]any{
			"uri":         uri,
			"diagnostics": diagnostics,
		},
	}
}

// completionIndex returns the script-local completion index for uri,
// building it on demand from the most recently compiled script and the
// current buffer lines and caching the result. It returns nil when no
// compiled script is available (for example, a never-parsed or
// size-limited document). The cache is invalidated by setDocument on
// every edit, so a cached index always matches the live buffer.
func (s *lspServer) completionIndex(uri string) *lspCompletionIndex {
	if index, ok := s.completions[uri]; ok {
		return index
	}
	script := s.compiled[uri]
	if script == nil {
		return nil
	}
	index := newLSPCompletionIndex(script, s.documentLines(uri))
	if s.completions != nil {
		s.completions[uri] = index
	}
	return index
}

func diagnosticsForSource(engine *vibes.Engine, source string) []map[string]any {
	_, _, _, diagnostics := compileForDiagnostics(engine, source)
	return diagnostics
}

// compileForDiagnostics parses once, compiles the parsed program when possible,
// and returns the AST so diagnostics and navigation caches stay in sync.
func compileForDiagnostics(engine *vibes.Engine, source string) (*vibes.Script, *ast.Program, []error, []map[string]any) {
	script, program, parseErrs, err := vibesruntime.CompileSnippetWithProgram(engine, source, scriptEntrypointFunction)
	if err == nil {
		return script, program, nil, []map[string]any{}
	}

	if len(parseErrs) > 0 {
		issues := vibes.ParseIssues(err)
		if len(issues) == 0 {
			return nil, program, parseErrs, []map[string]any{
				newDiagnostic(diagnosticRange{}, err.Error()),
			}
		}
		lines := strings.Split(source, "\n")
		out := make([]map[string]any, 0, len(issues))
		for _, issue := range issues {
			out = append(out, newDiagnostic(rangeForIssue(issue, lines), issue.Message))
		}
		return nil, program, parseErrs, out
	}

	issues := vibes.ParseIssues(err)
	if len(issues) == 0 {
		// Non-parse compile failures (size limits, duplicate top-level
		// names) carry no position; surface them at the document start.
		return nil, program, nil, []map[string]any{
			newDiagnostic(diagnosticRange{}, err.Error()),
		}
	}

	lines := strings.Split(source, "\n")
	out := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		out = append(out, newDiagnostic(rangeForIssue(issue, lines), issue.Message))
	}
	return nil, program, nil, out
}

// diagnosticRange is an LSP range in 0-indexed line/character offsets.
type diagnosticRange struct {
	startLine, startChar int
	endLine, endChar     int
}

// rangeForIssue converts a 1-indexed, rune-based parse issue to an LSP
// range in UTF-16 code units (the protocol's default position encoding).
// Issues without a known token span degrade to a single-rune range.
func rangeForIssue(issue vibes.ParseIssue, lines []string) diagnosticRange {
	startLine := max(0, issue.Pos.Line-1)
	startRune := max(0, issue.Pos.Column-1)
	r := diagnosticRange{
		startLine: startLine,
		startChar: utf16Character(lineAt(lines, startLine), startRune),
	}
	endLine, endRune := startLine, startRune+1
	if issue.End.Line >= issue.Pos.Line && (issue.End.Line > issue.Pos.Line || issue.End.Column > issue.Pos.Column) {
		endLine = issue.End.Line - 1
		endRune = max(0, issue.End.Column-1)
	}
	r.endLine = endLine
	r.endChar = utf16Character(lineAt(lines, endLine), endRune)
	return r
}

func lineAt(lines []string, idx int) string {
	if idx < 0 || idx >= len(lines) {
		return ""
	}
	return lines[idx]
}

// utf16Character converts a 0-indexed rune column within lineText to a
// UTF-16 code-unit offset. Columns beyond the line clamp to its full
// UTF-16 length plus the rune overshoot, so spans at end of line stay
// forward-progressing.
func utf16Character(lineText string, runeColumn int) int {
	units := 0
	runes := 0
	for _, r := range lineText {
		if runes >= runeColumn {
			return units
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
		runes++
	}
	return units + (runeColumn - runes)
}

func newDiagnostic(rng diagnosticRange, message string) map[string]any {
	if rng.endLine < rng.startLine || (rng.endLine == rng.startLine && rng.endChar <= rng.startChar) {
		rng.endLine = rng.startLine
		rng.endChar = rng.startChar + 1
	}
	return map[string]any{
		"range": map[string]any{
			"start": map[string]any{
				"line":      rng.startLine,
				"character": rng.startChar,
			},
			"end": map[string]any{
				"line":      rng.endLine,
				"character": rng.endChar,
			},
		},
		"severity": 1,
		"source":   "vibes-lsp",
		"message":  message,
	}
}

var (
	lspStaticCompletionItems = buildCompletionItems()
	lspStaticMemberItems     = buildMemberCompletionItems()
)

func completionItems() []map[string]any {
	return append([]map[string]any(nil), lspStaticCompletionItems...)
}

func buildCompletionItems() []map[string]any {
	labels := make([]string, 0, len(lspKeywords)+len(lspBuiltins))
	labels = append(labels, lspKeywords...)
	labels = append(labels, lspBuiltins...)
	sort.Strings(labels)

	keywordSet := make(map[string]struct{}, len(lspKeywords))
	for _, keyword := range lspKeywords {
		keywordSet[keyword] = struct{}{}
	}

	items := make([]map[string]any, 0, len(labels))
	for _, label := range labels {
		kind := 3 // Function
		detail := "builtin"
		if _, ok := keywordSet[label]; ok {
			kind = 14 // Keyword
			detail = "keyword"
		}
		items = append(items, map[string]any{
			"label":  label,
			"kind":   kind,
			"detail": detail,
		})
	}
	return items
}

func classifyWord(word string) string {
	if slices.Contains(lspKeywords, word) {
		return "keyword"
	}
	if slices.Contains(lspBuiltins, word) {
		return "builtin"
	}
	return "symbol"
}

func wordAtPosition(lines []string, line, character int) string {
	if line < 0 || line >= len(lines) {
		return ""
	}

	lineText := lines[line]
	runes := []rune(lineText)
	if len(runes) == 0 {
		return ""
	}
	if character < 0 {
		character = 0
	}
	character = min(utf16OffsetToRuneIndex(lineText, character), len(runes))

	cursor := character
	if cursor == len(runes) {
		cursor--
	}
	if cursor < 0 {
		return ""
	}
	if !isWordRune(runes[cursor]) {
		if cursor == 0 || !isWordRune(runes[cursor-1]) {
			return ""
		}
		cursor--
	}

	start := cursor
	for start > 0 && isWordRune(runes[start-1]) {
		start--
	}
	end := cursor
	for end < len(runes) && isWordRune(runes[end]) {
		end++
	}
	return string(runes[start:end])
}

func utf16OffsetToRuneIndex(text string, utf16Offset int) int {
	if utf16Offset <= 0 {
		return 0
	}
	runeIndex := 0
	consumed := 0
	for _, r := range text {
		if consumed >= utf16Offset {
			break
		}
		if r > 0xFFFF {
			consumed += 2
		} else {
			consumed++
		}
		runeIndex++
	}
	return runeIndex
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '?' || r == '!'
}

func (s *lspServer) readPayload() ([]byte, error) {
	contentLength := -1
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read header line: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if strings.EqualFold(name, "Content-Length") {
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %w", err)
			}
			contentLength = n
		}
	}

	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	if contentLength > maxLSPPayloadBytes {
		return nil, fmt.Errorf("Content-Length exceeds maximum (%d > %d bytes)", contentLength, maxLSPPayloadBytes)
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(s.reader, payload); err != nil {
		return nil, fmt.Errorf("read payload body: %w", err)
	}
	return payload, nil
}

func (s *lspServer) writePayload(msg lspOutboundMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	if _, err := fmt.Fprintf(s.writer, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := s.writer.Write(data); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	return s.writer.Flush()
}

// formattingEdits returns the TextEdit list for a formatting request:
// one full-document edit when the canonical formatter changes the
// source, or no edits when it is already formatted.
func formattingEdits(source string) []map[string]any {
	return formattingEditsForLines(source, splitLSPLines(source))
}

func formattingEditsForLines(source string, lines []string) []map[string]any {
	formatted := formatVibeSource(source)
	if formatted == source {
		return []map[string]any{}
	}
	lastLine := len(lines) - 1
	return []map[string]any{
		{
			"range": map[string]any{
				"start": map[string]any{
					"line":      0,
					"character": 0,
				},
				"end": map[string]any{
					"line":      lastLine,
					"character": utf16Character(lines[lastLine], len([]rune(lines[lastLine]))),
				},
			},
			"newText": formatted,
		},
	}
}

// splitLSPLines splits a document the way LSP clients count lines:
// "\r\n", bare "\n", and bare "\r" all terminate a line. Splitting on
// "\n" alone would understate the line count for CR-only documents and
// produce a replacement range clients reject or clamp.
func splitLSPLines(text string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			lines = append(lines, text[start:i])
			start = i + 1
		case '\r':
			lines = append(lines, text[start:i])
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	return append(lines, text[start:])
}

// completionItemsAt returns context-aware completion items: member
// methods when the cursor follows a "." receiver, otherwise keywords,
// builtins, user-defined functions, and the enclosing function's
// parameters and locals from the most recent successfully compiled
// version of the document.
func (s *lspServer) completionItemsAt(uri string, lines []string, line, character int) []map[string]any {
	if isMemberContext(lines, line, character) {
		return memberCompletionItems()
	}
	items := completionItems()
	if index := s.completionIndex(uri); index != nil {
		items = append(items, index.itemsAt(line)...)
	}
	return items
}

// isMemberContext reports whether the cursor sits immediately after a
// "." member access (allowing a partially typed member name). A dot
// inside a numeric literal ("1.5") does not count, but an empty or
// alphabetic suffix after a numeric receiver does — "1." and "1.days"
// are member accesses.
func isMemberContext(lines []string, line, character int) bool {
	if line < 0 || line >= len(lines) {
		return false
	}
	runes := []rune(lines[line])
	end := min(utf16OffsetToRuneIndex(lines[line], character), len(runes))
	start := end
	for start > 0 && isWordRune(runes[start-1]) {
		start--
	}
	if start == 0 || runes[start-1] != '.' {
		return false
	}
	if start >= 2 && unicode.IsDigit(runes[start-2]) {
		if start < end {
			// The dot is preceded by a digit and followed by a typed
			// suffix. When that suffix is the fractional/exponent tail of
			// a float literal (e.g. "1.5", "1.5e2", "1.5E6", "1.5e1_0")
			// the dot belongs to the number, not a member access.
			if isFractionSuffix(runes[start:end]) {
				return false
			}
		} else if start < len(runes) && unicode.IsDigit(runes[start]) {
			// The cursor sits between the dot and the fraction digits
			// of a numeric literal (e.g. 1.|5).
			return false
		}
	}
	return true
}

// isFractionSuffix reports whether suffix is the fractional (and optional
// exponent) tail of a float literal whose decimal point precedes it, such
// as the "5", "5e2", "5E6", or "5e1_0" in "1.5", "1.5e2", "1.5E6", and
// "1.5e1_0". The suffix is the run of word runes after the dot, so an
// exponent sign (which is not a word rune) never appears here; a trailing
// "e"/"E" without exponent digits is accepted so an in-progress literal
// like "1.5e" is still treated as a number rather than a member access.
// Underscores are accepted only between digits, matching the lexer's
// visual-separator rule.
func isFractionSuffix(suffix []rune) bool {
	if len(suffix) == 0 || !unicode.IsDigit(suffix[0]) {
		return false
	}
	i := consumeDigitsWithSeparators(suffix, 0)
	if i == len(suffix) {
		return true
	}
	if suffix[i] != 'e' && suffix[i] != 'E' {
		return false
	}
	i++
	if i == len(suffix) {
		// Trailing exponent marker with no digits yet (e.g. "5e").
		return true
	}
	if !unicode.IsDigit(suffix[i]) {
		return false
	}
	return consumeDigitsWithSeparators(suffix, i) == len(suffix)
}

// consumeDigitsWithSeparators advances past a run of digits beginning at
// index i, allowing underscores only when wedged between two digits, and
// returns the index of the first rune that is not part of the run.
func consumeDigitsWithSeparators(runes []rune, i int) int {
	for i < len(runes) {
		switch {
		case unicode.IsDigit(runes[i]):
			i++
		case runes[i] == '_' && i > 0 && unicode.IsDigit(runes[i-1]) &&
			i+1 < len(runes) && unicode.IsDigit(runes[i+1]):
			i++
		default:
			return i
		}
	}
	return i
}

// memberCompletionItems returns the type-unaware union of every builtin
// member method, labeled with the receiver types that provide it.
func memberCompletionItems() []map[string]any {
	return append([]map[string]any(nil), lspStaticMemberItems...)
}

func buildMemberCompletionItems() []map[string]any {
	byName := make(map[string][]string)
	for receiver, names := range vibes.MemberCompletionNames() {
		for _, name := range names {
			byName[name] = append(byName[name], receiver)
		}
	}
	labels := make([]string, 0, len(byName))
	for name := range byName {
		labels = append(labels, name)
	}
	sort.Strings(labels)

	items := make([]map[string]any, 0, len(labels))
	for _, label := range labels {
		receivers := byName[label]
		sort.Strings(receivers)
		items = append(items, map[string]any{
			"label":  label,
			"kind":   2, // Method
			"detail": strings.Join(receivers, ", "),
		})
	}
	return items
}

type lspCompletionIndex struct {
	functions []map[string]any
	scopes    []lspCompletionScope
}

type lspCompletionScope struct {
	startLine int
	endLine   int
	items     []map[string]any
	blocks    []lspCompletionBlock
}

type lspCompletionBlock struct {
	startLine int
	endLine   int
	items     []map[string]any
}

func newLSPCompletionIndex(script *vibes.Script, sourceLines []string) *lspCompletionIndex {
	if script == nil {
		return nil
	}
	index := &lspCompletionIndex{}
	functions := script.Functions()
	defLines := topLevelDefLines(sourceLines)
	index.functions = make([]map[string]any, 0, len(functions))
	index.scopes = make([]lspCompletionScope, 0, len(functions))
	for _, fn := range functions {
		if fn.Name == scriptEntrypointFunction {
			continue
		}
		index.functions = append(index.functions, map[string]any{
			"label":  fn.Name,
			"kind":   3, // Function
			"detail": "function",
		})
		// The cached AST may predate unparsable edits that shifted
		// lines, so anchor each function to its "def name" line in the
		// current buffer (duplicate names cannot compile) and fall
		// back to the cached position when the anchor is gone.
		start, ok := defLines[fn.Name]
		if !ok {
			start = fn.Pos.Line - 1
		}
		bodyExtent := lastStatementLine(fn.Body) - (fn.Pos.Line - 1)
		scope := lspCompletionScope{
			startLine: start,
			endLine:   functionEndLine(sourceLines, start, bodyExtent),
		}
		seen := make(map[string]struct{}, len(fn.Params))
		for _, param := range fn.Params {
			addLocalItem(&scope.items, seen, param.Name, "parameter")
		}
		for _, name := range localNames(fn.Body) {
			addLocalItem(&scope.items, seen, name, "local")
		}
		scope.blocks = rescueCompletionBlocks(fn.Body, fn.Pos.Line-1, start)
		sortCompletionItems(scope.items)
		index.scopes = append(index.scopes, scope)
	}
	sortCompletionItems(index.functions)
	return index
}

func topLevelDefLines(sourceLines []string) map[string]int {
	lines := make(map[string]int)
	for i, lineText := range sourceLines {
		decl := lineText
		for _, modifier := range []string{"export ", "private "} {
			if rest, ok := strings.CutPrefix(decl, modifier); ok {
				decl = rest
				break
			}
		}
		rest, ok := strings.CutPrefix(decl, "def ")
		if !ok {
			continue
		}
		nameEnd := strings.IndexFunc(rest, func(r rune) bool {
			return r == '(' || r == ' ' || r == '\t'
		})
		if nameEnd < 0 {
			nameEnd = len(rest)
		}
		if name := rest[:nameEnd]; name != "" {
			if _, exists := lines[name]; !exists {
				lines[name] = i
			}
		}
	}
	return lines
}

func (idx *lspCompletionIndex) itemsAt(line int) []map[string]any {
	if idx == nil {
		return nil
	}
	enclosing := -1
	for i, scope := range idx.scopes {
		if scope.startLine <= line && line <= scope.endLine &&
			(enclosing < 0 || scope.startLine > idx.scopes[enclosing].startLine) {
			enclosing = i
		}
	}
	items := make([]map[string]any, 0, len(idx.functions))
	items = append(items, idx.functions...)
	if enclosing >= 0 {
		items = append(items, idx.scopes[enclosing].items...)
		for _, block := range idx.scopes[enclosing].blocks {
			if block.startLine <= line && line <= block.endLine {
				items = append(items, block.items...)
			}
		}
	}
	return items
}

func sortCompletionItems(items []map[string]any) {
	sort.Slice(items, func(i, j int) bool {
		return items[i]["label"].(string) < items[j]["label"].(string)
	})
}

// functionEndLine estimates the 0-based line of the "end" closing a
// top-level function starting at startLine. It takes the further of
// two signals so neither failure mode truncates the scope: the first
// unindented "end" in the text (exact under canonical formatting, but
// an unformatted buffer can hold a flush-left inner terminator), and
// the last line any body statement occupies plus one (which covers
// every inner block but trails the real terminator across blank
// lines). A buffer with neither signal is treated as open-ended.
func functionEndLine(sourceLines []string, startLine, bodyExtent int) int {
	textualEnd := len(sourceLines)
	for i := startLine + 1; i < len(sourceLines); i++ {
		if strings.TrimRight(sourceLines[i], " \t") == "end" {
			textualEnd = i
			break
		}
	}
	if bodyExtent > 0 && textualEnd < len(sourceLines) {
		return max(textualEnd, startLine+bodyExtent)
	}
	return textualEnd
}

// lastStatementLine returns the greatest 0-based line covered by the
// statements, descending into nested control-flow bodies, plus one for
// the closing terminator.
func lastStatementLine(statements []ast.Statement) int {
	maxLine := 0
	var walk func([]ast.Statement)
	walk = func(stmts []ast.Statement) {
		for _, stmt := range stmts {
			if line := stmt.Pos().Line - 1; line > maxLine {
				maxLine = line
			}
			switch st := stmt.(type) {
			case *ast.ForStmt:
				walk(st.Body)
			case *ast.IfStmt:
				walk(st.Consequent)
				for _, elseIf := range st.ElseIf {
					walk([]ast.Statement{elseIf})
				}
				walk(st.Alternate)
			case *ast.WhileStmt:
				walk(st.Body)
			case *ast.UntilStmt:
				walk(st.Body)
			case *ast.TryStmt:
				walk(st.Body)
				walk(st.Else)
				walk(st.Rescue)
				walk(st.Ensure)
			}
		}
	}
	walk(statements)
	if maxLine == 0 {
		return 0
	}
	return maxLine + 1
}

func rescueCompletionBlocks(statements []ast.Statement, compiledFunctionStart, currentFunctionStart int) []lspCompletionBlock {
	var blocks []lspCompletionBlock
	var walk func([]ast.Statement)
	walk = func(stmts []ast.Statement) {
		for _, stmt := range stmts {
			switch st := stmt.(type) {
			case *ast.ForStmt:
				walk(st.Body)
			case *ast.IfStmt:
				walk(st.Consequent)
				for _, elseIf := range st.ElseIf {
					walk([]ast.Statement{elseIf})
				}
				walk(st.Alternate)
			case *ast.WhileStmt:
				walk(st.Body)
			case *ast.UntilStmt:
				walk(st.Body)
			case *ast.TryStmt:
				if st.RescueBinding != "" && st.RescuePosition.Line > 0 {
					startLine := currentFunctionStart + (st.RescuePosition.Line - 1 - compiledFunctionStart)
					endLine := startLine
					if rescueEnd := lastStatementLine(st.Rescue); rescueEnd > 0 {
						endLine = currentFunctionStart + (rescueEnd - compiledFunctionStart)
					}
					items := []map[string]any{}
					seen := map[string]struct{}{}
					addLocalItem(&items, seen, st.RescueBinding, "local")
					blocks = append(blocks, lspCompletionBlock{
						startLine: startLine,
						endLine:   endLine,
						items:     items,
					})
				}
				walk(st.Body)
				walk(st.Else)
				walk(st.Rescue)
				walk(st.Ensure)
			}
		}
	}
	walk(statements)
	return blocks
}

func addLocalItem(items *[]map[string]any, seen map[string]struct{}, name, detail string) {
	if name == "" {
		return
	}
	if _, dup := seen[name]; dup {
		return
	}
	seen[name] = struct{}{}
	*items = append(*items, map[string]any{
		"label":  name,
		"kind":   6, // Variable
		"detail": detail,
	})
}

// localNames collects the identifiers a statement list assigns,
// including loop variables and nested control-flow bodies.
func localNames(statements []ast.Statement) []string {
	var names []string
	var walkStmts func([]ast.Statement)
	walkStmts = func(stmts []ast.Statement) {
		for _, stmt := range stmts {
			switch st := stmt.(type) {
			case *ast.AssignStmt:
				appendAssignmentTargetNames(&names, st.Target)
			case *ast.ForStmt:
				names = append(names, st.Iterator)
				walkStmts(st.Body)
			case *ast.IfStmt:
				walkStmts(st.Consequent)
				for _, elseIf := range st.ElseIf {
					walkStmts([]ast.Statement{elseIf})
				}
				walkStmts(st.Alternate)
			case *ast.WhileStmt:
				walkStmts(st.Body)
			case *ast.UntilStmt:
				walkStmts(st.Body)
			case *ast.TryStmt:
				walkStmts(st.Body)
				walkStmts(st.Else)
				walkStmts(st.Rescue)
				walkStmts(st.Ensure)
			}
		}
	}
	walkStmts(statements)
	return names
}

func appendAssignmentTargetNames(names *[]string, target ast.Expression) {
	switch t := target.(type) {
	case *ast.Identifier:
		*names = append(*names, t.Name)
	case *ast.DestructureTarget:
		for _, element := range t.Elements {
			appendAssignmentTargetNames(names, element.Target)
		}
	}
}

// builtinSignatures maps global function builtins to their documented
// signatures. Entries are validated against the engine's registered
// builtins by tests so the table cannot go stale against renames.
var builtinSignatures = map[string]string{
	"assert":      "assert(condition, message = nil) -> nil",
	"money":       `money("12.34 USD") -> money`,
	"money_cents": "money_cents(cents, currency) -> money",
	"now":         "now -> string",
	"random_id":   "random_id(length = 16) -> string",
	"require":     `require(module, as: nil) -> object`,
	"to_float":    "to_float(value) -> float",
	"to_int":      "to_int(value) -> int",
	"uuid":        "uuid -> string",
}

// signatureHelpAt resolves the innermost call around the cursor and
// returns LSP SignatureHelp for it, or nil when no signature is known.
func (s *lspServer) signatureHelpAt(uri string, lines []string, line, character int) map[string]any {
	callee, activeParam, ok := enclosingCall(lines, line, character)
	if !ok {
		callee, activeParam, ok = parenlessCall(lines, line, character)
		if !ok {
			return nil
		}
	}

	if script := s.compiled[uri]; script != nil {
		if fn, found := script.Function(callee); found {
			paramLabels := make([]string, 0, len(fn.Params))
			for _, param := range fn.Params {
				paramLabels = append(paramLabels, paramLabel(param))
			}
			label := fn.Name + "(" + strings.Join(paramLabels, ", ") + ")"
			if fn.ReturnTy != nil {
				label += " -> " + ast.FormatTypeExpr(fn.ReturnTy)
			}
			return signatureHelpResponse(label, paramLabels, activeParam)
		}
	}
	if label, found := builtinSignatures[callee]; found {
		return signatureHelpResponse(label, paramLabelsFromSignature(label), activeParam)
	}
	return nil
}

func signatureHelpResponse(label string, paramLabels []string, activeParam int) map[string]any {
	parameters := make([]map[string]any, 0, len(paramLabels))
	for _, param := range paramLabels {
		parameters = append(parameters, map[string]any{"label": param})
	}
	if activeParam >= len(parameters) && len(parameters) > 0 {
		activeParam = len(parameters) - 1
	}
	return map[string]any{
		"signatures": []map[string]any{
			{
				"label":      label,
				"parameters": parameters,
			},
		},
		"activeSignature": 0,
		"activeParameter": activeParam,
	}
}

// enclosingCall scans the cursor's line backwards for the innermost
// unclosed call and reports the callee name and the zero-based argument
// index at the cursor. Multi-line calls degrade to no result.
func enclosingCall(lines []string, line, character int) (string, int, bool) {
	if line < 0 || line >= len(lines) {
		return "", 0, false
	}
	runes := []rune(lines[line])
	cursor := min(utf16OffsetToRuneIndex(lines[line], character), len(runes))
	masked := maskNonCode(runes[:cursor])

	parens, squares, braces := 0, 0, 0
	activeParam := 0
	for i := cursor - 1; i >= 0; i-- {
		switch masked[i] {
		case ')':
			parens++
		case ']':
			squares++
		case '}':
			braces++
		case '[':
			if squares > 0 {
				squares--
				continue
			}
			// The cursor sits inside an unclosed array literal, which
			// is a single argument: commas seen so far belong to it.
			activeParam = 0
		case '{':
			if braces > 0 {
				braces--
				continue
			}
			activeParam = 0
		case '(':
			if parens > 0 {
				parens--
				continue
			}
			end := i
			// The parser accepts whitespace between a callee and its
			// argument list, so skip it before extracting the word.
			for end > 0 && (runes[end-1] == ' ' || runes[end-1] == '\t') {
				end--
			}
			start := end
			for start > 0 && isWordRune(runes[start-1]) {
				start--
			}
			if start == end {
				return "", 0, false
			}
			if start > 0 && runes[start-1] == '.' {
				// Member call: no signature data exists for member
				// methods, and a same-named top-level function would
				// be the wrong hint.
				return "", 0, false
			}
			return string(runes[start:end]), activeParam, true
		case ',':
			if parens == 0 && squares == 0 && braces == 0 {
				activeParam++
			}
		}
	}
	return "", 0, false
}

// parenlessStatementBuiltins are the builtins the parser accepts in the
// no-paren statement form ("assert cond, msg"); only these produce
// signature help without an opening parenthesis.
var parenlessStatementBuiltins = map[string]struct{}{
	"assert": {},
}

// parenlessCall resolves the no-paren statement call form: the line's
// first word, when it is a paren-less-capable builtin followed by
// arguments, with the active argument counted by top-level commas.
func parenlessCall(lines []string, line, character int) (string, int, bool) {
	if line < 0 || line >= len(lines) {
		return "", 0, false
	}
	runes := []rune(lines[line])
	cursor := min(utf16OffsetToRuneIndex(lines[line], character), len(runes))
	masked := maskNonCode(runes[:cursor])

	start := 0
	for start < cursor && (masked[start] == ' ' || masked[start] == '\t') {
		start++
	}
	end := start
	for end < cursor && isWordRune(masked[end]) {
		end++
	}
	if end == start || end >= cursor {
		return "", 0, false
	}
	callee := string(masked[start:end])
	if _, ok := parenlessStatementBuiltins[callee]; !ok {
		return "", 0, false
	}
	if masked[end] != ' ' && masked[end] != '\t' {
		return "", 0, false
	}

	parens, squares, braces := 0, 0, 0
	activeParam := 0
	for i := end; i < cursor; i++ {
		switch masked[i] {
		case '(':
			parens++
		case ')':
			parens--
		case '[':
			squares++
		case ']':
			squares--
		case '{':
			braces++
		case '}':
			braces--
		case ',':
			if parens == 0 && squares == 0 && braces == 0 {
				activeParam++
			}
		}
	}
	return callee, activeParam, true
}

// maskNonCode blanks string literals and the trailing comment so
// structural scans only see code. Strings are masked first because a
// "#" inside one is not a comment.
func maskNonCode(runes []rune) []rune {
	masked := maskStringLiterals(runes)
	for i, r := range masked {
		if r == '#' {
			for j := i; j < len(masked); j++ {
				masked[j] = ' '
			}
			break
		}
	}
	return masked
}

// maskStringLiterals replaces quoted string literals, including quotes,
// contents, and escapes, with spaces, so structural scans do not trip on
// commas, parentheses, or brackets inside them. An unterminated literal
// masks through to the end.
func maskStringLiterals(runes []rune) []rune {
	masked := make([]rune, len(runes))
	var quote rune
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			masked[i] = ' '
			if r == '\\' && i+1 < len(runes) {
				i++
				masked[i] = ' '
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			masked[i] = ' '
			continue
		}
		masked[i] = r
	}
	return masked
}

// paramLabel renders one parameter: its name, type annotation when
// present, and a default marker when the parameter is optional. An optional
// keyword-only parameter spells its default directly after the colon
// (`name: …`), matching its source form, whereas positional defaults use the
// `= …` marker.
func paramLabel(param ast.Param) string {
	if param.Kind == ast.ParamKeyword && param.DefaultVal != nil && param.Type == nil {
		return ast.FormatParamTarget(param) + " …"
	}
	label := ast.FormatParamTarget(param)
	if param.Type != nil {
		label += ": " + ast.FormatTypeExpr(param.Type)
	}
	if param.DefaultVal != nil {
		label += " = …"
	}
	return label
}

func paramLabelsFromSignature(label string) []string {
	open := strings.Index(label, "(")
	close := strings.LastIndex(label, ")")
	if open < 0 || close <= open+1 {
		return nil
	}
	parts := strings.Split(label[open+1:close], ",")
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		labels = append(labels, strings.TrimSpace(part))
	}
	return labels
}

// definitionLocation resolves word to the position of its top-level
// definition (function, class, enum, or enum member) in the document.
// Setter methods are declared as "name=" while the cursor word at an
// assignment call site is bare "name", so the setter form is matched
// when no exact definition exists.
func definitionLocation(program *ast.Program, uri string, sourceLines []string, word string) map[string]any {
	if program == nil || word == "" {
		return nil
	}
	for _, candidate := range []string{word, word + "="} {
		if location := exactDefinitionLocation(program, uri, sourceLines, candidate); location != nil {
			return location
		}
	}
	return nil
}

func exactDefinitionLocation(program *ast.Program, uri string, sourceLines []string, word string) map[string]any {
	for _, stmt := range program.Statements {
		switch st := stmt.(type) {
		case *ast.FunctionStmt:
			if st.Name == word {
				return anchoredLocation(uri, sourceLines, st.Position, st.Name)
			}
		case *ast.ClassStmt:
			if st.Name == word {
				return anchoredLocation(uri, sourceLines, st.Position, st.Name)
			}
			for _, method := range st.Methods {
				if method.Name == word {
					return anchoredLocation(uri, sourceLines, method.Position, method.Name)
				}
			}
			for _, method := range st.ClassMethods {
				if method.Name == word {
					return anchoredLocation(uri, sourceLines, method.Position, method.Name)
				}
			}
		case *ast.EnumStmt:
			if st.Name == word {
				return anchoredLocation(uri, sourceLines, st.Position, st.Name)
			}
			for _, member := range st.Members {
				if member.Name == word {
					return anchoredLocation(uri, sourceLines, member.Position, member.Name)
				}
			}
		}
	}
	return nil
}

// anchoredLocation builds a Location for a declaration after
// re-anchoring it in the current buffer; a declaration the buffer no
// longer contains yields nil rather than a stale jump target.
func anchoredLocation(uri string, sourceLines []string, pos ast.Position, name string) map[string]any {
	line := anchorDeclLine(sourceLines, pos, name)
	if line < 0 {
		return nil
	}
	return locationAt(uri, sourceLines, line, name)
}

// anchorDeclLine finds the 0-based line currently declaring name,
// preferring the parser-recorded line when it still matches and
// searching the whole buffer otherwise. -1 means the declaration no
// longer exists in the live text.
func anchorDeclLine(sourceLines []string, pos ast.Position, name string) int {
	recorded := pos.Line - 1
	if declLineMatches(sourceLines, recorded, name) {
		return recorded
	}
	for i := range sourceLines {
		if declLineMatches(sourceLines, i, name) {
			return i
		}
	}
	return -1
}

// declLineMatches reports whether the line declares name: a def, class,
// or enum keyword (allowing export/private modifiers and self. method
// receivers), or a bare enum-member identifier.
func declLineMatches(sourceLines []string, line int, name string) bool {
	if line < 0 || line >= len(sourceLines) {
		return false
	}
	text := strings.TrimSpace(sourceLines[line])
	for _, modifier := range []string{"export ", "private "} {
		text = strings.TrimPrefix(text, modifier)
	}
	for _, keyword := range []string{"def ", "class ", "enum "} {
		rest, ok := strings.CutPrefix(text, keyword)
		if !ok {
			continue
		}
		rest = strings.TrimPrefix(rest, "self.")
		if !strings.HasPrefix(rest, name) {
			return false
		}
		tail := []rune(rest[len(name):])
		return len(tail) == 0 || !isWordRune(tail[0])
	}
	return text == name
}

// locationAt builds a Location whose range covers the declared name on
// the given 0-based line. Parser positions point at the declaration
// keyword, so the name's own column is recovered from the line text.
// Characters are UTF-16 code units.
func locationAt(uri string, sourceLines []string, line int, name string) map[string]any {
	lineText := ""
	if line < len(sourceLines) {
		lineText = sourceLines[line]
	}
	bare := strings.TrimSuffix(name, "=")
	startRune := 0
	if col := findWordColumn(lineText, bare, 0); col >= 0 {
		startRune = col
	}
	startChar := utf16Character(lineText, startRune)
	endChar := utf16Character(lineText, startRune+len([]rune(bare)))
	if endChar <= startChar {
		endChar = startChar + 1
	}
	return map[string]any{
		"uri": uri,
		"range": map[string]any{
			"start": map[string]any{"line": line, "character": startChar},
			"end":   map[string]any{"line": line, "character": endChar},
		},
	}
}

// findWordColumn returns the rune column of the first whole-word
// occurrence of name at or after fromRune, or -1 when absent.
func findWordColumn(lineText, name string, fromRune int) int {
	if name == "" {
		return -1
	}
	runes := []rune(lineText)
	nameRunes := []rune(name)
	for i := max(0, fromRune); i+len(nameRunes) <= len(runes); i++ {
		if string(runes[i:i+len(nameRunes)]) != name {
			continue
		}
		if i > 0 && isWordRune(runes[i-1]) {
			continue
		}
		if next := i + len(nameRunes); next < len(runes) && isWordRune(runes[next]) {
			continue
		}
		return i
	}
	return -1
}

// lspPosition is an LSP position in 0-indexed line / UTF-16 character
// offsets. It marshals to the protocol's {"line","character"} object.
type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// lspRange is an LSP range over two positions.
type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

// lspDocumentSymbol is one entry in a textDocument/documentSymbol
// outline. The selection range covers the declaration line, while the
// full range extends to the last child so LSP clients can nest
// breadcrumbs and match the cursor to the enclosing symbol. Children is
// optional in the protocol and omitted when empty, so a leaf symbol
// (function, method, or enum member) carries no children field. A typed
// struct avoids the per-symbol nested map[string]any allocations that
// dominated large-document outlines.
type lspDocumentSymbol struct {
	Name           string              `json:"name"`
	Kind           int                 `json:"kind"`
	Range          lspRange            `json:"range"`
	SelectionRange lspRange            `json:"selectionRange"`
	Children       []lspDocumentSymbol `json:"children,omitempty"`
}

// documentSymbols renders the document outline: top-level functions,
// classes with their methods, and enums with their members.
func documentSymbols(program *ast.Program, sourceLines []string) []lspDocumentSymbol {
	if program == nil {
		return []lspDocumentSymbol{}
	}
	symbols := make([]lspDocumentSymbol, 0, len(program.Statements))
	appendSymbol := func(dst []lspDocumentSymbol, name string, kind int, pos ast.Position, anchorName string, children []lspDocumentSymbol) []lspDocumentSymbol {
		line := anchorDeclLine(sourceLines, pos, anchorName)
		if line < 0 {
			// The declaration is gone from the live buffer; a stale
			// outline entry would point into unrelated text.
			return dst
		}
		return append(dst, symbolFor(name, kind, line, sourceLines, children))
	}
	for _, stmt := range program.Statements {
		switch st := stmt.(type) {
		case *ast.FunctionStmt:
			symbols = appendSymbol(symbols, st.Name, 12, st.Position, st.Name, nil)
		case *ast.ClassStmt:
			children := make([]lspDocumentSymbol, 0, len(st.Methods)+len(st.ClassMethods))
			for _, method := range st.Methods {
				children = appendSymbol(children, method.Name, 6, method.Position, method.Name, nil)
			}
			for _, method := range st.ClassMethods {
				children = appendSymbol(children, "self."+method.Name, 6, method.Position, method.Name, nil)
			}
			symbols = appendSymbol(symbols, st.Name, 5, st.Position, st.Name, children)
		case *ast.EnumStmt:
			children := make([]lspDocumentSymbol, 0, len(st.Members))
			for _, member := range st.Members {
				children = appendSymbol(children, member.Name, 22, member.Position, member.Name, nil)
			}
			symbols = appendSymbol(symbols, st.Name, 10, st.Position, st.Name, children)
		}
	}
	return symbols
}

// symbolFor builds one DocumentSymbol. The selection range covers the
// declaration line, while the full range extends to the last child so
// LSP clients can nest breadcrumbs and match the cursor to the
// enclosing symbol.
func symbolFor(name string, kind, line int, sourceLines []string, children []lspDocumentSymbol) lspDocumentSymbol {
	lineText := ""
	if line < len(sourceLines) {
		lineText = sourceLines[line]
	}
	endChar := utf16Character(lineText, len([]rune(lineText)))
	selection := lspRange{
		Start: lspPosition{Line: line, Character: 0},
		End:   lspPosition{Line: line, Character: endChar},
	}

	endLine := line
	for _, child := range children {
		if child.Range.End.Line > endLine {
			endLine = child.Range.End.Line
			endChar = child.Range.End.Character
		}
	}
	return lspDocumentSymbol{
		Name:           name,
		Kind:           kind,
		Range:          lspRange{Start: lspPosition{Line: line, Character: 0}, End: lspPosition{Line: endLine, Character: endChar}},
		SelectionRange: selection,
		Children:       children,
	}
}
