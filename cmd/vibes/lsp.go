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
}

func runLSP() error {
	server := &lspServer{
		reader: bufio.NewReader(os.Stdin),
		writer: bufio.NewWriter(os.Stdout),
		engine: vibes.MustNewEngine(vibes.Config{}),
		docs:   make(map[string]string),
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
						"completionProvider": map[string]any{
							"resolveProvider": false,
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
	case "textDocument/completion":
		if incoming.ID == nil {
			return nil
		}
		return []lspOutboundMessage{
			{
				JSONRPC: "2.0",
				ID:      incoming.ID,
				Result: map[string]any{
					"isIncomplete": false,
					"items":        completionItems(),
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
	return lspOutboundMessage{
		JSONRPC: "2.0",
		Method:  "textDocument/publishDiagnostics",
		Params: map[string]any{
			"uri":         uri,
			"diagnostics": diagnosticsForSource(s.engine, source),
		},
	}
}

func diagnosticsForSource(engine *vibes.Engine, source string) []map[string]any {
	_, err := engine.Compile(source)
	if err == nil {
		return []map[string]any{}
	}

	issues := vibes.ParseIssues(err)
	if len(issues) == 0 {
		// Non-parse compile failures (size limits, duplicate top-level
		// names) carry no position; surface them at the document start.
		return []map[string]any{
			newDiagnostic(diagnosticRange{}, err.Error()),
		}
	}

	lines := strings.Split(source, "\n")
	out := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		out = append(out, newDiagnostic(rangeForIssue(issue, lines), issue.Message))
	}
	return out
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
