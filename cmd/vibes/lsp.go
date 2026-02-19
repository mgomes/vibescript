package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/mgomes/vibescript/vibes"
)

var parseErrorPattern = regexp.MustCompile(`parse error at ([0-9]+):([0-9]+): ([^\n]+)`)

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
			if err == io.EOF {
				return nil
			}
			return err
		}

		var incoming lspInboundMessage
		if err := json.Unmarshal(payload, &incoming); err != nil {
			continue
		}

		messages := s.handleMessage(incoming)
		for _, msg := range messages {
			if err := s.writePayload(msg); err != nil {
				return err
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
						"textDocumentSync": 1,
						"hoverProvider":    true,
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
		return []lspOutboundMessage{{JSONRPC: "2.0", ID: incoming.ID, Result: nil}}
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
				{JSONRPC: "2.0", ID: incoming.ID, Result: nil},
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
						"value": fmt.Sprintf("`%s`\n\nVibeScript %s", word, kind),
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

	matches := parseErrorPattern.FindAllStringSubmatch(err.Error(), -1)
	if len(matches) == 0 {
		return []map[string]any{
			newDiagnostic(0, 0, err.Error()),
		}
	}

	out := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		line, _ := strconv.Atoi(match[1])
		column, _ := strconv.Atoi(match[2])
		message := match[3]
		lineIdx := max(0, line-1)
		colIdx := max(0, column-1)
		out = append(out, newDiagnostic(lineIdx, colIdx, message))
	}
	return out
}

func newDiagnostic(line, character int, message string) map[string]any {
	return map[string]any{
		"range": map[string]any{
			"start": map[string]any{
				"line":      line,
				"character": character,
			},
			"end": map[string]any{
				"line":      line,
				"character": character + 1,
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
	for _, keyword := range lspKeywords {
		if keyword == word {
			return "keyword"
		}
	}
	for _, builtin := range lspBuiltins {
		if builtin == word {
			return "builtin"
		}
	}
	return "symbol"
}

func wordAtPosition(source string, line, character int) string {
	lines := strings.Split(source, "\n")
	if line < 0 || line >= len(lines) {
		return ""
	}

	runes := []rune(lines[line])
	if len(runes) == 0 {
		return ""
	}
	if character < 0 {
		character = 0
	}
	if character > len(runes) {
		character = len(runes)
	}

	cursor := character
	if cursor == len(runes) {
		cursor--
	}
	if cursor < 0 {
		return ""
	}
	if !isWordRune(runes[cursor]) {
		if cursor > 0 && isWordRune(runes[cursor-1]) {
			cursor--
		} else {
			return ""
		}
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

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '?' || r == '!'
}

func (s *lspServer) readPayload() ([]byte, error) {
	contentLength := -1
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return nil, err
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
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(s.reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (s *lspServer) writePayload(msg lspOutboundMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.writer, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	if _, err := s.writer.Write(data); err != nil {
		return err
	}
	return s.writer.Flush()
}
