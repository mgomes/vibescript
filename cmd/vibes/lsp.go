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
	"github.com/mgomes/vibescript/vibes"
)

const maxLSPPayloadBytes = 8 << 20

// jsonNull is the explicit JSON null used for intentionally empty
// results. lspOutboundMessage.Result is omitempty so notifications can
// share the struct, which means a plain nil would drop the result field
// entirely and produce a response with neither result nor error —
// invalid JSON-RPC that strict clients reject.
var jsonNull = json.RawMessage("null")

var lspKeywords = []string{
	"and",
	"break",
	"class",
	"def",
	"else",
	"elsif",
	"end",
	"false",
	"for",
	"if",
	"in",
	"next",
	"nil",
	"or",
	"raise",
	"require",
	"rescue",
	"return",
	"true",
	"unless",
	"until",
	"while",
}

var lspBuiltins = []string{
	"assert",
	"money",
	"money_cents",
	"now",
	"random_id",
	"to_float",
	"to_int",
	"uuid",
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
	// compiled holds the most recent successfully compiled script per
	// document, so completion can offer user-defined symbols while the
	// buffer is mid-edit and temporarily unparsable.
	compiled map[string]*vibes.Script
}

func runLSP() error {
	server := &lspServer{
		reader:   bufio.NewReader(os.Stdin),
		writer:   bufio.NewWriter(os.Stdout),
		engine:   vibes.MustNewEngine(vibes.Config{}),
		docs:     make(map[string]string),
		compiled: make(map[string]*vibes.Script),
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
		s.docs[params.TextDocument.URI] = params.TextDocument.Text
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
		s.docs[params.TextDocument.URI] = latest
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
				Result:  formattingEdits(source),
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
		source := s.docs[params.TextDocument.URI]
		help := s.signatureHelpAt(params.TextDocument.URI, source, params.Position.Line, params.Position.Character)
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
		source := s.docs[params.TextDocument.URI]
		items := s.completionItemsAt(params.TextDocument.URI, source, params.Position.Line, params.Position.Character)
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
		source := s.docs[params.TextDocument.URI]
		word := wordAtPosition(source, params.Position.Line, params.Position.Character)
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

func (s *lspServer) publishDiagnostics(uri, source string) lspOutboundMessage {
	script, diagnostics := compileForDiagnostics(s.engine, source)
	if script != nil && s.compiled != nil {
		s.compiled[uri] = script
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

func diagnosticsForSource(engine *vibes.Engine, source string) []map[string]any {
	_, diagnostics := compileForDiagnostics(engine, source)
	return diagnostics
}

// compileForDiagnostics compiles once, returning the script (nil when
// compilation failed) alongside the diagnostics payload.
func compileForDiagnostics(engine *vibes.Engine, source string) (*vibes.Script, []map[string]any) {
	script, err := engine.Compile(source)
	if err == nil {
		return script, []map[string]any{}
	}

	issues := vibes.ParseIssues(err)
	if len(issues) == 0 {
		// Non-parse compile failures (size limits, duplicate top-level
		// names) carry no position; surface them at the document start.
		return nil, []map[string]any{
			newDiagnostic(diagnosticRange{}, err.Error()),
		}
	}

	lines := strings.Split(source, "\n")
	out := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		out = append(out, newDiagnostic(rangeForIssue(issue, lines), issue.Message))
	}
	return nil, out
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

func completionItems() []map[string]any {
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

func wordAtPosition(source string, line, character int) string {
	lines := strings.Split(source, "\n")
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
	formatted := formatVibeSource(source)
	if formatted == source {
		return []map[string]any{}
	}
	lines := splitLSPLines(source)
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
func (s *lspServer) completionItemsAt(uri, source string, line, character int) []map[string]any {
	if isMemberContext(source, line, character) {
		return memberCompletionItems()
	}
	items := completionItems()
	items = append(items, scriptCompletionItems(s.compiled[uri], splitLSPLines(source), line)...)
	return items
}

// isMemberContext reports whether the cursor sits immediately after a
// "." member access (allowing a partially typed member name). A dot
// inside a numeric literal ("1.5") does not count, but an empty or
// alphabetic suffix after a numeric receiver does — "1." and "1.days"
// are member accesses.
func isMemberContext(source string, line, character int) bool {
	lines := splitLSPLines(source)
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
			partialIsDigits := true
			for _, r := range runes[start:end] {
				if !unicode.IsDigit(r) {
					partialIsDigits = false
					break
				}
			}
			if partialIsDigits {
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

// memberCompletionItems returns the type-unaware union of every builtin
// member method, labeled with the receiver types that provide it.
func memberCompletionItems() []map[string]any {
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

// scriptCompletionItems offers the script's function names plus the
// parameters and locals of the function enclosing the cursor line.
func scriptCompletionItems(script *vibes.Script, sourceLines []string, line int) []map[string]any {
	if script == nil {
		return nil
	}
	var items []map[string]any
	functions := script.Functions()
	enclosing := -1
	enclosingStart := -1
	for i, fn := range functions {
		items = append(items, map[string]any{
			"label":  fn.Name,
			"kind":   3, // Function
			"detail": "function",
		})
		// The cached AST may predate unparsable edits that shifted
		// lines, so anchor each function to its "def name" line in the
		// current buffer (duplicate names cannot compile) and fall
		// back to the cached position when the anchor is gone.
		start := findDefLine(sourceLines, fn.Name)
		if start < 0 {
			start = fn.Pos.Line - 1
		}
		bodyExtent := lastStatementLine(fn.Body) - (fn.Pos.Line - 1)
		if start <= line && line <= functionEndLine(sourceLines, start, bodyExtent) &&
			(enclosing < 0 || start > enclosingStart) {
			enclosing = i
			enclosingStart = start
		}
	}
	if enclosing >= 0 {
		fn := functions[enclosing]
		seen := make(map[string]struct{})
		for _, param := range fn.Params {
			addLocalItem(&items, seen, param.Name, "parameter")
		}
		for _, name := range localNames(fn.Body) {
			addLocalItem(&items, seen, name, "local")
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["label"].(string) < items[j]["label"].(string)
	})
	return items
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

// findDefLine locates the 0-based line declaring the named top-level
// function in the current buffer, or -1 when absent. Only unindented
// declarations qualify — indented defs are class methods, which may
// share a top-level function's name — but the export and private
// modifiers that can decorate a top-level def are accepted. A missed
// anchor falls back to the cached position rather than mis-anchoring.
func findDefLine(sourceLines []string, name string) int {
	target := "def " + name
	for i, lineText := range sourceLines {
		decl := lineText
		for _, modifier := range []string{"export ", "private "} {
			if rest, ok := strings.CutPrefix(decl, modifier); ok {
				decl = rest
				break
			}
		}
		if !strings.HasPrefix(decl, target) {
			continue
		}
		rest := strings.TrimRight(decl[len(target):], " \t")
		if rest == "" || strings.HasPrefix(rest, "(") || strings.HasPrefix(rest, " ") {
			return i
		}
	}
	return -1
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
				if ident, ok := st.Target.(*ast.Identifier); ok {
					names = append(names, ident.Name)
				}
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
				walkStmts(st.Rescue)
				walkStmts(st.Ensure)
			}
		}
	}
	walkStmts(statements)
	return names
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
func (s *lspServer) signatureHelpAt(uri, source string, line, character int) map[string]any {
	callee, activeParam, ok := enclosingCall(source, line, character)
	if !ok {
		return nil
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
func enclosingCall(source string, line, character int) (string, int, bool) {
	lines := splitLSPLines(source)
	if line < 0 || line >= len(lines) {
		return "", 0, false
	}
	runes := []rune(lines[line])
	cursor := min(utf16OffsetToRuneIndex(lines[line], character), len(runes))
	masked := maskStringLiterals(runes[:cursor])

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
			start := end
			for start > 0 && isWordRune(runes[start-1]) {
				start--
			}
			if start == end {
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

// maskStringLiterals replaces double-quoted string literals — quotes,
// contents, and escapes — with spaces, so structural scans do not trip
// on commas, parentheses, or brackets inside them. An unterminated
// literal masks through to the end.
func maskStringLiterals(runes []rune) []rune {
	masked := make([]rune, len(runes))
	inString := false
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if inString {
			masked[i] = ' '
			if r == '\\' && i+1 < len(runes) {
				i++
				masked[i] = ' '
				continue
			}
			if r == '"' {
				inString = false
			}
			continue
		}
		if r == '"' {
			inString = true
			masked[i] = ' '
			continue
		}
		masked[i] = r
	}
	return masked
}

// paramLabel renders one parameter: its name, type annotation when
// present, and a default marker when the parameter is optional.
func paramLabel(param ast.Param) string {
	label := param.Name
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
