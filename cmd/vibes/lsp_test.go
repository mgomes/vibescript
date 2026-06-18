package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
	"github.com/mgomes/vibescript/vibes"
)

func TestRunCLIStartsLSPAndExitsOnEOF(t *testing.T) {
	// not parallel-safe: swaps process-wide os.Stdin
	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close write pipe: %v", err)
	}
	os.Stdin = r
	defer func() {
		os.Stdin = origStdin
		if err := r.Close(); err != nil {
			t.Errorf("close read pipe: %v", err)
		}
	}()

	if err := runCLI([]string{"vibes", "lsp"}); err != nil {
		t.Fatalf("runCLI lsp failed: %v", err)
	}
}

func TestDiagnosticsForSourceWithoutErrors(t *testing.T) {
	t.Parallel()
	engine := vibes.MustNewEngine(vibes.Config{})
	source := "def run()\n  1\nend\n"
	diags := diagnosticsForSource(engine, source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %d", len(diags))
	}
}

func TestDiagnosticsForSourceWithParseError(t *testing.T) {
	t.Parallel()
	engine := vibes.MustNewEngine(vibes.Config{})
	source := "def run(\n  1\nend\n"
	diags := diagnosticsForSource(engine, source)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics for invalid source")
	}
	first := diags[0]
	if first["severity"] != 1 {
		t.Fatalf("expected severity 1, got %#v", first["severity"])
	}
	message, ok := first["message"].(string)
	if !ok || message == "" {
		t.Fatalf("expected non-empty diagnostic message, got %#v", first["message"])
	}
}

func TestDiagnosticsForSourceSpanOffendingToken(t *testing.T) {
	t.Parallel()
	engine := vibes.MustNewEngine(vibes.Config{})
	// "123" is the offending token: line 1, columns 5-7 (0-indexed 4-7).
	diags := diagnosticsForSource(engine, "def 123()\n  1\nend\n")
	if len(diags) == 0 {
		t.Fatal("expected diagnostics for invalid function name")
	}

	rng, ok := diags[0]["range"].(map[string]any)
	if !ok {
		t.Fatalf("diagnostic range missing: %#v", diags[0])
	}
	start := rng["start"].(map[string]any)
	end := rng["end"].(map[string]any)
	if start["line"] != 0 || start["character"] != 4 {
		t.Fatalf("start = %#v, want line 0 character 4", start)
	}
	if end["line"] != 0 || end["character"] != 7 {
		t.Fatalf("end = %#v, want line 0 character 7 (token span, not zero-width point)", end)
	}
	if diags[0]["message"] != "expected function name, got integer" {
		t.Fatalf("message = %#v, want bare parser message", diags[0]["message"])
	}
}

func TestDiagnosticsForSourceFallBackToPointRangeAtEOF(t *testing.T) {
	t.Parallel()
	engine := vibes.MustNewEngine(vibes.Config{})
	diags := diagnosticsForSource(engine, "def run()\n  x = [1,\nend\n")
	if len(diags) < 2 {
		t.Fatalf("expected multiple diagnostics, got %d", len(diags))
	}

	for _, diag := range diags {
		rng := diag["range"].(map[string]any)
		start := rng["start"].(map[string]any)
		end := rng["end"].(map[string]any)
		startLine, startChar := start["line"].(int), start["character"].(int)
		endLine, endChar := end["line"].(int), end["character"].(int)
		if endLine < startLine || (endLine == startLine && endChar <= startChar) {
			t.Fatalf("diagnostic range is not forward-progressing: %#v", rng)
		}
	}
}

func TestDiagnosticsForSourceUseUTF16CharacterOffsets(t *testing.T) {
	t.Parallel()
	engine := vibes.MustNewEngine(vibes.Config{})
	// Each emoji is one rune but two UTF-16 code units. The offending
	// token "2" sits at rune column 16 (1-indexed) on line 2; two
	// non-BMP runes precede it, so the UTF-16 offset is 17.
	diags := diagnosticsForSource(engine, "def run()\n  x = [\"\U0001F600\U0001F600\", 1 2]\nend\n")
	if len(diags) == 0 {
		t.Fatal("expected diagnostics for malformed array literal")
	}

	rng := diags[0]["range"].(map[string]any)
	start := rng["start"].(map[string]any)
	end := rng["end"].(map[string]any)
	if start["line"] != 1 || start["character"] != 17 {
		t.Fatalf("start = %#v, want line 1 character 17 (UTF-16 units)", start)
	}
	if end["line"] != 1 || end["character"] != 18 {
		t.Fatalf("end = %#v, want line 1 character 18 spanning the token", end)
	}
}

func TestDiagnosticsForSourceWithoutPositionsReportDocumentStart(t *testing.T) {
	t.Parallel()
	engine := vibes.MustNewEngine(vibes.Config{})
	diags := diagnosticsForSource(engine, "def run()\n  1\nend\n\ndef run()\n  2\nend\n")
	if len(diags) != 1 {
		t.Fatalf("expected single positionless diagnostic, got %d", len(diags))
	}
	rng := diags[0]["range"].(map[string]any)
	start := rng["start"].(map[string]any)
	if start["line"] != 0 || start["character"] != 0 {
		t.Fatalf("positionless diagnostic start = %#v, want document start", start)
	}
	if diags[0]["message"] != "duplicate function run" {
		t.Fatalf("message = %#v, want compile error text", diags[0]["message"])
	}
}

func TestCompletionItemsAreSortedAndCategorized(t *testing.T) {
	t.Parallel()
	items := completionItems()
	if len(items) == 0 {
		t.Fatalf("expected completion items")
	}

	labels := make([]string, 0, len(items))
	for _, item := range items {
		label, ok := item["label"].(string)
		if !ok {
			t.Fatalf("unexpected completion label: %#v", item["label"])
		}
		labels = append(labels, label)
	}
	if !slices.IsSorted(labels) {
		t.Fatalf("expected sorted completion labels, got %v", labels)
	}

	keyword := findCompletionItem(t, items, "if")
	if keyword["detail"] != "keyword" {
		t.Fatalf("expected keyword detail, got %#v", keyword["detail"])
	}
	if keyword["kind"] != 14 {
		t.Fatalf("expected keyword kind 14, got %#v", keyword["kind"])
	}

	builtin := findCompletionItem(t, items, "assert")
	if builtin["detail"] != "builtin" {
		t.Fatalf("expected builtin detail, got %#v", builtin["detail"])
	}
	if builtin["kind"] != 3 {
		t.Fatalf("expected builtin kind 3, got %#v", builtin["kind"])
	}
}

func TestLSPKeywordCompletionsMatchParserKeywords(t *testing.T) {
	t.Parallel()
	items := completionItems()
	got := make([]string, 0, len(ast.Keywords()))
	for _, item := range items {
		if item["detail"] != "keyword" {
			continue
		}
		label, ok := item["label"].(string)
		if !ok {
			t.Fatalf("unexpected keyword completion label: %#v", item["label"])
		}
		got = append(got, label)
	}

	want := ast.Keywords()
	if !slices.Equal(got, want) {
		t.Fatalf("keyword completions = %#v, want parser keywords %#v", got, want)
	}
	for _, unsupported := range []string{"and", "or", "unless"} {
		if classifyWord(unsupported) == "keyword" {
			t.Fatalf("classifyWord(%q) = keyword, want unsupported syntax excluded from keyword completions", unsupported)
		}
	}

	require := findCompletionItem(t, items, "require")
	if require["detail"] != "builtin" {
		t.Fatalf("require detail = %#v, want builtin", require["detail"])
	}
	if require["kind"] != 3 {
		t.Fatalf("require kind = %#v, want function kind 3", require["kind"])
	}
}

func TestHandleMessageDidOpenPublishesDiagnostics(t *testing.T) {
	t.Parallel()
	server := &lspServer{
		engine: vibes.MustNewEngine(vibes.Config{}),
		docs:   make(map[string]string),
	}
	params := map[string]any{
		"textDocument": map[string]any{
			"uri":  "file:///tmp/test.vibe",
			"text": "def run(\n  1\nend\n",
		},
	}
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	messages := server.handleMessage(lspInboundMessage{
		JSONRPC: "2.0",
		Method:  "textDocument/didOpen",
		Params:  payload,
	})
	if len(messages) != 1 {
		t.Fatalf("expected one publishDiagnostics notification, got %d", len(messages))
	}
	if messages[0].Method != "textDocument/publishDiagnostics" {
		t.Fatalf("unexpected method: %q", messages[0].Method)
	}
	paramsMap, ok := messages[0].Params.(map[string]any)
	if !ok {
		t.Fatalf("unexpected params payload: %#v", messages[0].Params)
	}
	diags, ok := paramsMap["diagnostics"].([]map[string]any)
	if !ok {
		t.Fatalf("unexpected diagnostics payload: %#v", paramsMap["diagnostics"])
	}
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics for invalid source")
	}
}

func TestHandleMessageHoverClassifiesBuiltins(t *testing.T) {
	t.Parallel()
	server := &lspServer{
		engine: vibes.MustNewEngine(vibes.Config{}),
		docs: map[string]string{
			"file:///tmp/test.vibe": "def run()\n  assert(true)\nend\n",
		},
	}
	params := map[string]any{
		"textDocument": map[string]any{
			"uri": "file:///tmp/test.vibe",
		},
		"position": map[string]any{
			"line":      1,
			"character": 3,
		},
	}
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	messages := server.handleMessage(lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("1"),
		Method:  "textDocument/hover",
		Params:  payload,
	})
	if len(messages) != 1 {
		t.Fatalf("expected one response, got %d", len(messages))
	}
	result, ok := messages[0].Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected hover result: %#v", messages[0].Result)
	}
	contents, ok := result["contents"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected hover contents: %#v", result["contents"])
	}
	value, ok := contents["value"].(string)
	if !ok {
		t.Fatalf("unexpected hover value: %#v", contents["value"])
	}
	if !strings.Contains(value, "builtin") {
		t.Fatalf("expected builtin classification in hover value, got %q", value)
	}
}

func TestWordAtPosition(t *testing.T) {
	t.Parallel()
	source := "def run()\n  to_int(\"1\")\nend\n"
	word := wordAtPosition(splitLSPLines(source), 1, 4)
	if word != "to_int" {
		t.Fatalf("expected to_int, got %q", word)
	}
}

func TestWordAtPositionUsesUTF16CharacterOffsets(t *testing.T) {
	t.Parallel()
	source := "😀😀x y\n"
	word := wordAtPosition(splitLSPLines(source), 0, 4)
	if word != "x" {
		t.Fatalf("expected x, got %q", word)
	}
}

func TestReadPayloadAllowsJSONFramingAboveSourceLimit(t *testing.T) {
	t.Parallel()
	source := strings.Repeat("\n", 1<<20)
	params := map[string]any{
		"textDocument": map[string]any{
			"uri":  "file:///tmp/large.vibe",
			"text": source,
		},
	}
	rawParams, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal(params) failed: %v", err)
	}
	msg := lspInboundMessage{
		JSONRPC: "2.0",
		Method:  "textDocument/didOpen",
		Params:  rawParams,
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("json.Marshal(lsp message) failed: %v", err)
	}
	if len(payload) <= 1<<20 {
		t.Fatalf("framed LSP payload length = %d, want larger than source limit", len(payload))
	}

	wire := append([]byte("Content-Length: "+strconv.Itoa(len(payload))+"\r\n\r\n"), payload...)
	server := &lspServer{reader: bufio.NewReader(bytes.NewReader(wire))}
	got, err := server.readPayload()
	if err != nil {
		t.Fatalf("lspServer.readPayload(%d-byte framed source) failed: %v", len(payload), err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("lspServer.readPayload(%d-byte framed source) returned mismatched payload", len(payload))
	}
}

func rawID(value string) *json.RawMessage {
	raw := json.RawMessage(value)
	return &raw
}

func findCompletionItem(t *testing.T, items []map[string]any, label string) map[string]any {
	t.Helper()
	for _, item := range items {
		itemLabel, ok := item["label"].(string)
		if ok && itemLabel == label {
			return item
		}
	}
	t.Fatalf("missing completion item %q", label)
	return nil
}

func TestHandleMessageFormattingReturnsFullDocumentEdit(t *testing.T) {
	t.Parallel()
	server := &lspServer{
		engine: vibes.MustNewEngine(vibes.Config{}),
		docs: map[string]string{
			"file:///tmp/fmt.vibe": "def run()  \n  1\t\nend",
		},
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": "file:///tmp/fmt.vibe"},
	}
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	messages := server.handleMessage(lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("7"),
		Method:  "textDocument/formatting",
		Params:  payload,
	})
	if len(messages) != 1 {
		t.Fatalf("expected one response, got %d", len(messages))
	}
	edits, ok := messages[0].Result.([]map[string]any)
	if !ok || len(edits) != 1 {
		t.Fatalf("expected one text edit, got %#v", messages[0].Result)
	}
	if edits[0]["newText"] != "def run()\n  1\nend\n" {
		t.Fatalf("newText = %q, want canonical formatting", edits[0]["newText"])
	}
	rng := edits[0]["range"].(map[string]any)
	start := rng["start"].(map[string]any)
	end := rng["end"].(map[string]any)
	if start["line"] != 0 || start["character"] != 0 {
		t.Fatalf("start = %#v, want document start", start)
	}
	if end["line"] != 2 || end["character"] != 3 {
		t.Fatalf("end = %#v, want end of last line (2:3)", end)
	}
}

func TestHandleMessageFormattingAlreadyFormatted(t *testing.T) {
	t.Parallel()
	server := &lspServer{
		engine: vibes.MustNewEngine(vibes.Config{}),
		docs: map[string]string{
			"file:///tmp/clean.vibe": "def run()\n  1\nend\n",
		},
	}
	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": "file:///tmp/clean.vibe"},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	messages := server.handleMessage(lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("8"),
		Method:  "textDocument/formatting",
		Params:  payload,
	})
	edits, ok := messages[0].Result.([]map[string]any)
	if !ok || len(edits) != 0 {
		t.Fatalf("expected zero edits for formatted doc, got %#v", messages[0].Result)
	}
}

func TestHandleMessageFormattingUnknownDocument(t *testing.T) {
	t.Parallel()
	server := &lspServer{
		engine: vibes.MustNewEngine(vibes.Config{}),
		docs:   map[string]string{},
	}
	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": "file:///tmp/missing.vibe"},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	messages := server.handleMessage(lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("9"),
		Method:  "textDocument/formatting",
		Params:  payload,
	})
	if len(messages) != 1 {
		t.Fatalf("expected one response, got %d", len(messages))
	}
	payload, err = json.Marshal(messages[0])
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if !strings.Contains(string(payload), `"result":null`) {
		t.Fatalf("response %s must carry an explicit null result", payload)
	}
	if strings.Contains(string(payload), `"error"`) {
		t.Fatalf("response %s must not carry an error", payload)
	}
}

func TestFormattingEditsHandleBareCarriageReturns(t *testing.T) {
	t.Parallel()
	// "a\rb\r" is three client-visible lines (the last empty); the edit
	// range must end at line 2 character 0, not line 0 character 4.
	edits := formattingEdits("a\rb\r")
	if len(edits) != 1 {
		t.Fatalf("expected one edit, got %#v", edits)
	}
	if edits[0]["newText"] != "a\nb\n" {
		t.Fatalf("newText = %q, want normalized line endings", edits[0]["newText"])
	}
	end := edits[0]["range"].(map[string]any)["end"].(map[string]any)
	if end["line"] != 2 || end["character"] != 0 {
		t.Fatalf("end = %#v, want line 2 character 0", end)
	}
}

func TestSplitLSPLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want []string
	}{
		{name: "lf", text: "a\nb", want: []string{"a", "b"}},
		{name: "crlf", text: "a\r\nb", want: []string{"a", "b"}},
		{name: "bare_cr", text: "a\rb\r", want: []string{"a", "b", ""}},
		{name: "mixed", text: "a\r\nb\rc\n", want: []string{"a", "b", "c", ""}},
		{name: "empty", text: "", want: []string{""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitLSPLines(tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("splitLSPLines(%q) = %q, want %q", tc.text, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitLSPLines(%q)[%d] = %q, want %q", tc.text, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func openDoc(t *testing.T, server *lspServer, uri, text string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": text},
	})
	if err != nil {
		t.Fatalf("marshal didOpen: %v", err)
	}
	server.handleMessage(lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: payload})
}

func completionLabels(t *testing.T, server *lspServer, uri string, line, character int) map[string]map[string]any {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": character},
	})
	if err != nil {
		t.Fatalf("marshal completion params: %v", err)
	}
	messages := server.handleMessage(lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("21"),
		Method:  "textDocument/completion",
		Params:  payload,
	})
	if len(messages) != 1 {
		t.Fatalf("expected one completion response, got %d", len(messages))
	}
	result := messages[0].Result.(map[string]any)
	items := result["items"].([]map[string]any)
	labels := make(map[string]map[string]any, len(items))
	for _, item := range items {
		labels[item["label"].(string)] = item
	}
	return labels
}

func newCompletionTestServer() *lspServer {
	return &lspServer{
		engine:   vibes.MustNewEngine(vibes.Config{}),
		docs:     make(map[string]string),
		lines:    make(map[string][]string),
		compiled: make(map[string]*vibes.Script),
		programs: make(map[string]*ast.Program),
	}
}

func TestCompletionAfterDotOffersMemberMethods(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/members.vibe"
	openDoc(t, server, uri, "def run()\n  \"abc\".\nend\n")

	labels := completionLabels(t, server, uri, 1, 8)
	upcase, ok := labels["upcase"]
	if !ok {
		t.Fatalf("member completion missing upcase: %d items", len(labels))
	}
	if upcase["kind"] != 2 {
		t.Fatalf("upcase kind = %#v, want method kind 2", upcase["kind"])
	}
	if !strings.Contains(upcase["detail"].(string), "string") {
		t.Fatalf("upcase detail = %#v, want receiver types", upcase["detail"])
	}
	if _, hasKeyword := labels["def"]; hasKeyword {
		t.Fatal("member completion must not offer keywords")
	}
	if _, hasArray := labels["flatten"]; !hasArray {
		t.Fatal("member completion should be the type-unaware union (missing array method flatten)")
	}
}

func TestCompletionAfterDotWithPartialWord(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/partial.vibe"
	openDoc(t, server, uri, "def run()\n  \"abc\".upc\nend\n")

	labels := completionLabels(t, server, uri, 1, 11)
	if _, ok := labels["upcase"]; !ok {
		t.Fatal("partial member word should still complete members")
	}
}

func TestCompletionOffersFunctionsParamsAndLocals(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/scope.vibe"
	openDoc(t, server, uri, `def helper(amount)
  doubled = amount * 2
  doubled
end

def run()
  total = helper(2)
  total
end
`)

	labels := completionLabels(t, server, uri, 1, 2)
	for label, wantDetail := range map[string]string{
		"helper":  "function",
		"run":     "function",
		"amount":  "parameter",
		"doubled": "local",
		"if":      "keyword",
		"assert":  "builtin",
	} {
		item, ok := labels[label]
		if !ok {
			t.Fatalf("completion missing %q", label)
		}
		if item["detail"] != wantDetail {
			t.Fatalf("%q detail = %#v, want %q", label, item["detail"], wantDetail)
		}
	}
	if _, leaked := labels["total"]; leaked {
		t.Fatal("locals from another function must not leak into scope")
	}

	inRun := completionLabels(t, server, uri, 6, 2)
	if _, ok := inRun["total"]; !ok {
		t.Fatal("locals of the enclosing function should be offered")
	}
}

func TestCompletionSurvivesUnparsableEdits(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/midedit.vibe"
	openDoc(t, server, uri, "def helper()\n  1\nend\n")

	payload, err := json.Marshal(map[string]any{
		"textDocument":   map[string]any{"uri": uri},
		"contentChanges": []map[string]any{{"text": "def helper()\n  1\nend\n\ndef broken("}},
	})
	if err != nil {
		t.Fatalf("marshal didChange: %v", err)
	}
	server.handleMessage(lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: payload})

	labels := completionLabels(t, server, uri, 4, 0)
	if _, ok := labels["helper"]; !ok {
		t.Fatal("functions from the last good compile should survive mid-edit breakage")
	}
}

func TestIsMemberContext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		source    string
		line, chr int
		want      bool
	}{
		{name: "right_after_dot", source: "x.", line: 0, chr: 2, want: true},
		{name: "partial_member", source: "x.up", line: 0, chr: 4, want: true},
		{name: "no_dot", source: "xup", line: 0, chr: 3, want: false},
		{name: "dot_then_space", source: "x. y", line: 0, chr: 4, want: false},
		{name: "out_of_range_line", source: "x.", line: 5, chr: 1, want: false},
		{name: "float_literal", source: "1.5", line: 0, chr: 3, want: false},
		{name: "cursor_inside_float", source: "1.5", line: 0, chr: 2, want: false},
		{name: "numeric_member_open", source: "1.", line: 0, chr: 2, want: true},
		{name: "numeric_member_word", source: "1.days", line: 0, chr: 6, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMemberContext(splitLSPLines(tc.source), tc.line, tc.chr); got != tc.want {
				t.Fatalf("isMemberContext(%q, %d, %d) = %v, want %v", tc.source, tc.line, tc.chr, got, tc.want)
			}
		})
	}
}

func TestCompletionDoesNotLeakLocalsBetweenFunctions(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/gaps.vibe"
	openDoc(t, server, uri, `def first(alpha)
  beta = alpha
  beta
end

def second()
  1
end
`)

	// Line 4 is the blank line between the two functions.
	between := completionLabels(t, server, uri, 4, 0)
	for _, leaked := range []string{"alpha", "beta"} {
		if _, ok := between[leaked]; ok {
			t.Fatalf("local %q leaked into the gap between functions", leaked)
		}
	}
	if _, ok := between["first"]; !ok {
		t.Fatal("function names should still be offered between functions")
	}

	// Inside first's body the locals are available.
	inside := completionLabels(t, server, uri, 1, 2)
	for _, want := range []string{"alpha", "beta"} {
		if _, ok := inside[want]; !ok {
			t.Fatalf("local %q missing inside its function", want)
		}
	}
}

func signatureHelpResult(t *testing.T, server *lspServer, uri string, line, character int) any {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": character},
	})
	if err != nil {
		t.Fatalf("marshal signatureHelp params: %v", err)
	}
	messages := server.handleMessage(lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("31"),
		Method:  "textDocument/signatureHelp",
		Params:  payload,
	})
	if len(messages) != 1 {
		t.Fatalf("expected one signatureHelp response, got %d", len(messages))
	}
	return messages[0].Result
}

func TestSignatureHelpForUserFunction(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/sig.vibe"
	openDoc(t, server, uri, `def charge(amount: int, currency = "USD", note: string? = nil) -> money
  money_cents(amount, currency)
end

def run()
  charge(100, "USD")
end
`)

	result, ok := signatureHelpResult(t, server, uri, 5, 14).(map[string]any)
	if !ok {
		t.Fatal("expected signature help for user function")
	}
	signatures := result["signatures"].([]map[string]any)
	if len(signatures) != 1 {
		t.Fatalf("expected one signature, got %d", len(signatures))
	}
	label := signatures[0]["label"].(string)
	if !strings.Contains(label, "charge(amount: int, currency = …, note: string? = …)") {
		t.Fatalf("label = %q, want params with types and default markers", label)
	}
	if !strings.HasSuffix(label, "-> money") {
		t.Fatalf("label = %q, want return type suffix", label)
	}
	if result["activeParameter"] != 1 {
		t.Fatalf("activeParameter = %#v, want 1 after the first comma", result["activeParameter"])
	}
	params := signatures[0]["parameters"].([]map[string]any)
	if len(params) != 3 {
		t.Fatalf("expected 3 parameter labels, got %d", len(params))
	}
}

func TestSignatureHelpForBuiltin(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/sigb.vibe"
	openDoc(t, server, uri, "def run()\n  money_cents(\nend\n")

	result, ok := signatureHelpResult(t, server, uri, 1, 14).(map[string]any)
	if !ok {
		t.Fatal("expected signature help for builtin")
	}
	signatures := result["signatures"].([]map[string]any)
	label := signatures[0]["label"].(string)
	if !strings.Contains(label, "money_cents(cents, currency)") {
		t.Fatalf("label = %q, want curated builtin signature", label)
	}
	if result["activeParameter"] != 0 {
		t.Fatalf("activeParameter = %#v, want 0", result["activeParameter"])
	}
}

func TestSignatureHelpOutsideCallReturnsNull(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/signo.vibe"
	openDoc(t, server, uri, "def run()\n  x = 1\nend\n")

	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 1, "character": 7},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	messages := server.handleMessage(lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("32"),
		Method:  "textDocument/signatureHelp",
		Params:  payload,
	})
	wire, err := json.Marshal(messages[0])
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if !strings.Contains(string(wire), `"result":null`) {
		t.Fatalf("response %s, want explicit null result", wire)
	}
}

func TestEnclosingCall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		source    string
		line, chr int
		callee    string
		param     int
		ok        bool
	}{
		{name: "just_opened", source: "charge(", line: 0, chr: 7, callee: "charge", param: 0, ok: true},
		{name: "after_comma", source: "charge(1, ", line: 0, chr: 10, callee: "charge", param: 1, ok: true},
		{name: "nested_call", source: "outer(inner(1, 2), ", line: 0, chr: 12, callee: "inner", param: 0, ok: true},
		{name: "after_nested_close", source: "outer(inner(1, 2), ", line: 0, chr: 19, callee: "outer", param: 1, ok: true},
		{name: "closed_call", source: "charge(1)", line: 0, chr: 9, ok: false},
		{name: "no_call", source: "x = 1", line: 0, chr: 5, ok: false},
		{name: "grouping_paren", source: "(1 + 2, ", line: 0, chr: 8, ok: false},
		{name: "array_arg_commas_ignored", source: "charge([1, 2], ", line: 0, chr: 15, callee: "charge", param: 1, ok: true},
		{name: "hash_arg_commas_ignored", source: "charge({a: 1, b: 2}, ", line: 0, chr: 21, callee: "charge", param: 1, ok: true},
		{name: "string_comma_ignored", source: `charge("1,00", `, line: 0, chr: 15, callee: "charge", param: 1, ok: true},
		{name: "string_paren_ignored", source: `charge("a)b", `, line: 0, chr: 14, callee: "charge", param: 1, ok: true},
		{name: "single_string_comma_ignored", source: `charge('1,00', `, line: 0, chr: 15, callee: "charge", param: 1, ok: true},
		{name: "single_string_paren_ignored", source: `charge('a)b', `, line: 0, chr: 14, callee: "charge", param: 1, ok: true},
		{name: "cursor_inside_array_literal", source: "charge([1, ", line: 0, chr: 11, callee: "charge", param: 0, ok: true},
		{name: "member_call_suppressed", source: "price.format(", line: 0, chr: 13, ok: false},
		{name: "comment_suppressed", source: "# money_cents(", line: 0, chr: 14, ok: false},
		{name: "space_before_paren", source: "charge (100, ", line: 0, chr: 13, callee: "charge", param: 1, ok: true},
		{name: "hash_in_string_not_comment", source: `charge("#", `, line: 0, chr: 12, callee: "charge", param: 1, ok: true},
		{name: "hash_in_single_string_not_comment", source: `charge('#', `, line: 0, chr: 12, callee: "charge", param: 1, ok: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			callee, param, ok := enclosingCall(splitLSPLines(tc.source), tc.line, tc.chr)
			if ok != tc.ok {
				t.Fatalf("enclosingCall(%q) ok = %v, want %v", tc.source, ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if callee != tc.callee || param != tc.param {
				t.Fatalf("enclosingCall(%q) = (%q, %d), want (%q, %d)", tc.source, callee, param, tc.callee, tc.param)
			}
		})
	}
}

func TestParenlessCall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		source    string
		line, chr int
		callee    string
		param     int
		ok        bool
	}{
		{name: "after_comma", source: "assert true, ", line: 0, chr: 13, callee: "assert", param: 1, ok: true},
		{name: "single_string_comma_ignored", source: `assert true, 'a,b'`, line: 0, chr: 18, callee: "assert", param: 1, ok: true},
		{name: "single_string_hash_not_comment", source: `assert true, 'a#b', `, line: 0, chr: 20, callee: "assert", param: 2, ok: true},
		{name: "comment_suppressed", source: "# assert true, ", line: 0, chr: 15, ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			callee, param, ok := parenlessCall(splitLSPLines(tc.source), tc.line, tc.chr)
			if ok != tc.ok {
				t.Fatalf("parenlessCall(%q) ok = %v, want %v", tc.source, ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if callee != tc.callee || param != tc.param {
				t.Fatalf("parenlessCall(%q) = (%q, %d), want (%q, %d)", tc.source, callee, param, tc.callee, tc.param)
			}
		})
	}
}

func TestBuiltinSignaturesMatchRegisteredBuiltins(t *testing.T) {
	t.Parallel()
	builtins := vibes.MustNewEngine(vibes.Config{}).Builtins()
	for name := range builtinSignatures {
		if _, ok := builtins[name]; !ok {
			t.Errorf("builtinSignatures entry %q does not correspond to a registered builtin", name)
		}
	}
}

const navigationFixture = `def helper(n)
  n * 2
end

class Wallet
  def balance()
    1
  end

  def self.empty()
    Wallet.new
  end
end

enum Status
  Draft
  Published
end

def run()
  helper(1)
end
`

func TestDefinitionResolvesTopLevelSymbols(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/nav.vibe"
	openDoc(t, server, uri, navigationFixture)

	// "helper" inside run() on line 20.
	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 20, "character": 4},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	messages := server.handleMessage(lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("41"),
		Method:  "textDocument/definition",
		Params:  payload,
	})
	location, ok := messages[0].Result.(map[string]any)
	if !ok {
		t.Fatalf("expected location, got %#v", messages[0].Result)
	}
	if location["uri"] != uri {
		t.Fatalf("uri = %#v, want same document", location["uri"])
	}
	start := location["range"].(map[string]any)["start"].(map[string]any)
	if start["line"] != 0 {
		t.Fatalf("definition line = %#v, want 0", start["line"])
	}
}

func TestDefinitionResolvesEnumMembers(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/nav-enum.vibe"
	openDoc(t, server, uri, navigationFixture)

	location := definitionLocation(server.programs[uri], uri, server.documentLines(uri), "Published")
	if location == nil {
		t.Fatal("expected location for enum member")
	}
	start := location["range"].(map[string]any)["start"].(map[string]any)
	if start["line"] != 16 {
		t.Fatalf("Published line = %#v, want 16", start["line"])
	}
}

func TestDefinitionUnknownSymbolReturnsNull(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/nav-null.vibe"
	openDoc(t, server, uri, navigationFixture)

	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 1, "character": 3},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	messages := server.handleMessage(lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("42"),
		Method:  "textDocument/definition",
		Params:  payload,
	})
	wire, err := json.Marshal(messages[0])
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if !strings.Contains(string(wire), `"result":null`) {
		t.Fatalf("response %s, want explicit null", wire)
	}
}

func TestDocumentSymbolsOutline(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/outline.vibe"
	openDoc(t, server, uri, navigationFixture)

	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	messages := server.handleMessage(lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("43"),
		Method:  "textDocument/documentSymbol",
		Params:  payload,
	})
	symbols, ok := messages[0].Result.([]map[string]any)
	if !ok {
		t.Fatalf("expected symbol list, got %#v", messages[0].Result)
	}
	byName := map[string]map[string]any{}
	for _, symbol := range symbols {
		byName[symbol["name"].(string)] = symbol
	}
	if len(symbols) != 4 {
		t.Fatalf("expected 4 top-level symbols, got %d", len(symbols))
	}
	if byName["helper"]["kind"] != 12 || byName["run"]["kind"] != 12 {
		t.Fatal("functions should have kind 12")
	}
	wallet := byName["Wallet"]
	if wallet["kind"] != 5 {
		t.Fatalf("Wallet kind = %#v, want class kind 5", wallet["kind"])
	}
	walletChildren := wallet["children"].([]map[string]any)
	childNames := make([]string, 0, len(walletChildren))
	for _, child := range walletChildren {
		childNames = append(childNames, child["name"].(string))
	}
	if !slices.Contains(childNames, "balance") || !slices.Contains(childNames, "self.empty") {
		t.Fatalf("Wallet children = %v, want balance and self.empty", childNames)
	}
	status := byName["Status"]
	if status["kind"] != 10 {
		t.Fatalf("Status kind = %#v, want enum kind 10", status["kind"])
	}
	if members := status["children"].([]map[string]any); len(members) != 2 {
		t.Fatalf("Status members = %d, want 2", len(members))
	}
}

func TestDocumentSymbolsSurviveMidEditParses(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/outline-midedit.vibe"
	openDoc(t, server, uri, navigationFixture)

	payload, err := json.Marshal(map[string]any{
		"textDocument":   map[string]any{"uri": uri},
		"contentChanges": []map[string]any{{"text": navigationFixture + "\ndef broken("}},
	})
	if err != nil {
		t.Fatalf("marshal didChange: %v", err)
	}
	server.handleMessage(lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: payload})

	if location := definitionLocation(server.programs[uri], uri, server.documentLines(uri), "helper"); location == nil {
		t.Fatal("navigation should survive a mid-edit broken parse")
	}
}

func TestInitializeAdvertisesDotCompletionTrigger(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	messages := server.handleMessage(lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("51"),
		Method:  "initialize",
	})
	caps := messages[0].Result.(map[string]any)["capabilities"].(map[string]any)
	completion := caps["completionProvider"].(map[string]any)
	triggers, ok := completion["triggerCharacters"].([]string)
	if !ok || !slices.Contains(triggers, ".") {
		t.Fatalf("completion triggerCharacters = %#v, want to include \".\"", completion["triggerCharacters"])
	}
}

func TestCompletionScopeSurvivesFlushLeftInnerEnd(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/flushleft.vibe"
	// The inner "end" is unindented (legal but non-canonical), so a
	// text-only scan would truncate first's scope at line 4.
	openDoc(t, server, uri, `def first(alpha)
  if alpha > 1
    beta = alpha
end
  gamma = alpha
  gamma
end
`)

	labels := completionLabels(t, server, uri, 4, 2)
	for _, want := range []string{"alpha", "gamma"} {
		if _, ok := labels[want]; !ok {
			t.Fatalf("local %q missing below a flush-left inner end", want)
		}
	}
}

func TestCompletionScopesSurviveLineShiftsWhileUnparsable(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/shifted.vibe"
	openDoc(t, server, uri, `def first(alpha)
  beta = alpha
  beta
end
`)

	// Three comment lines above shift the function down, and the broken
	// def at the bottom keeps the buffer unparsable, so positions must
	// re-anchor against the current text.
	payload, err := json.Marshal(map[string]any{
		"textDocument":   map[string]any{"uri": uri},
		"contentChanges": []map[string]any{{"text": "# one\n# two\n# three\ndef first(alpha)\n  beta = alpha\n  beta\nend\n\ndef broken("}},
	})
	if err != nil {
		t.Fatalf("marshal didChange: %v", err)
	}
	server.handleMessage(lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: payload})

	inside := completionLabels(t, server, uri, 4, 2)
	for _, want := range []string{"alpha", "beta"} {
		if _, ok := inside[want]; !ok {
			t.Fatalf("local %q missing after lines shifted under an unparsable edit", want)
		}
	}

	above := completionLabels(t, server, uri, 0, 0)
	if _, leaked := above["beta"]; leaked {
		t.Fatal("locals leaked above the shifted function")
	}
}

func TestCompletionAnchorIgnoresSameNamedClassMethod(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/shadowed-def.vibe"
	openDoc(t, server, uri, `class Wallet
  def total(cents)
    cents
  end
end

def total(amount)
  rounded = amount
  rounded
end
`)

	inside := completionLabels(t, server, uri, 7, 2)
	for _, want := range []string{"amount", "rounded"} {
		if _, ok := inside[want]; !ok {
			t.Fatalf("local %q missing: top-level def anchored to the class method", want)
		}
	}
	if _, leaked := inside["cents"]; leaked {
		t.Fatal("class method parameter leaked into the top-level function scope")
	}
}

func TestCompletionAnchorsDecoratedTopLevelDefs(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/decorated.vibe"
	openDoc(t, server, uri, `private def secret(token)
  hashed = token
  hashed
end
`)

	// Shift the function down with comments while keeping the buffer
	// unparsable, so the anchor must match the decorated declaration.
	payload, err := json.Marshal(map[string]any{
		"textDocument":   map[string]any{"uri": uri},
		"contentChanges": []map[string]any{{"text": "# one\n# two\nprivate def secret(token)\n  hashed = token\n  hashed\nend\n\ndef broken("}},
	})
	if err != nil {
		t.Fatalf("marshal didChange: %v", err)
	}
	server.handleMessage(lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: payload})

	inside := completionLabels(t, server, uri, 3, 2)
	for _, want := range []string{"token", "hashed"} {
		if _, ok := inside[want]; !ok {
			t.Fatalf("local %q missing: decorated def did not re-anchor", want)
		}
	}
	above := completionLabels(t, server, uri, 0, 0)
	if _, leaked := above["hashed"]; leaked {
		t.Fatal("locals leaked above the shifted decorated function")
	}
}

func TestSignatureHelpForParenlessAssert(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/parenless.vibe"
	openDoc(t, server, uri, "def run()\n  assert 1 == 1, \"ok\"\nend\n")

	result, ok := signatureHelpResult(t, server, uri, 1, 17).(map[string]any)
	if !ok {
		t.Fatal("expected signature help for paren-less assert")
	}
	label := result["signatures"].([]map[string]any)[0]["label"].(string)
	if !strings.Contains(label, "assert(condition, message = nil)") {
		t.Fatalf("label = %q, want assert signature", label)
	}
	if result["activeParameter"] != 1 {
		t.Fatalf("activeParameter = %#v, want 1 after the comma", result["activeParameter"])
	}
}

func TestNavigationCacheClearsWhenSymbolsRemoved(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/cleared.vibe"
	openDoc(t, server, uri, navigationFixture)

	payload, err := json.Marshal(map[string]any{
		"textDocument":   map[string]any{"uri": uri},
		"contentChanges": []map[string]any{{"text": "# nothing here\n"}},
	})
	if err != nil {
		t.Fatalf("marshal didChange: %v", err)
	}
	server.handleMessage(lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: payload})

	if location := definitionLocation(server.programs[uri], uri, server.documentLines(uri), "helper"); location != nil {
		t.Fatal("definition still resolves after a clean parse removed every symbol")
	}
	if symbols := documentSymbols(server.programs[uri], server.documentLines(uri)); len(symbols) != 0 {
		t.Fatalf("outline = %d symbols, want none after a clean empty parse", len(symbols))
	}
}

func TestDefinitionResolvesSetterMethods(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/setter.vibe"
	openDoc(t, server, uri, `class Counter
  def value=(n)
    @value = n
  end
end

def run()
  c = Counter.new
  c.value = 3
end
`)

	location := definitionLocation(server.programs[uri], uri, server.documentLines(uri), "value")
	if location == nil {
		t.Fatal("expected setter definition for bare assignment word")
	}
	start := location["range"].(map[string]any)["start"].(map[string]any)
	if start["line"] != 1 {
		t.Fatalf("setter definition line = %#v, want 1", start["line"])
	}
}

func TestDefinitionRangeCoversTheName(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/namerange.vibe"
	openDoc(t, server, uri, navigationFixture)

	location := definitionLocation(server.programs[uri], uri, server.documentLines(uri), "helper")
	rng := location["range"].(map[string]any)
	start := rng["start"].(map[string]any)
	end := rng["end"].(map[string]any)
	// Line 0 is `def helper(n)`: the name spans characters 4-10.
	if start["character"] != 4 || end["character"] != 10 {
		t.Fatalf("range = %#v..%#v, want the name span 4..10", start, end)
	}
}

func TestDocumentSymbolParentRangesEncloseChildren(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/enclose.vibe"
	openDoc(t, server, uri, navigationFixture)

	symbols := documentSymbols(server.programs[uri], server.documentLines(uri))
	for _, symbol := range symbols {
		children, ok := symbol["children"].([]map[string]any)
		if !ok {
			continue
		}
		parentEnd := symbol["range"].(map[string]any)["end"].(map[string]any)["line"].(int)
		for _, child := range children {
			childEnd := child["range"].(map[string]any)["end"].(map[string]any)["line"].(int)
			if childEnd > parentEnd {
				t.Fatalf("%s child %s ends at line %d outside parent end %d",
					symbol["name"], child["name"], childEnd, parentEnd)
			}
		}
	}
}

func TestNavigationDropsSymbolsMissingFromLiveBuffer(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/replaced.vibe"
	openDoc(t, server, uri, navigationFixture)

	// Replace the whole buffer with an unparsable fragment: the cached
	// AST survives, but none of its declarations exist in the live text.
	payload, err := json.Marshal(map[string]any{
		"textDocument":   map[string]any{"uri": uri},
		"contentChanges": []map[string]any{{"text": "def broken("}},
	})
	if err != nil {
		t.Fatalf("marshal didChange: %v", err)
	}
	server.handleMessage(lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: payload})

	lines := server.documentLines(uri)
	if location := definitionLocation(server.programs[uri], uri, lines, "helper"); location != nil {
		t.Fatalf("definition resolved into unrelated text: %#v", location)
	}
	if symbols := documentSymbols(server.programs[uri], lines); len(symbols) != 0 {
		t.Fatalf("outline = %d symbols for a buffer containing none of them", len(symbols))
	}
}

func TestDocumentLinesCacheRefreshesOnChange(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/cache.vibe"
	openDoc(t, server, uri, "def old\n  1\nend\n")
	if got := server.documentLines(uri)[0]; got != "def old" {
		t.Fatalf("documentLines(%q)[0] after open = %q, want %q", uri, got, "def old")
	}

	payload, err := json.Marshal(map[string]any{
		"textDocument":   map[string]any{"uri": uri},
		"contentChanges": []map[string]any{{"text": "def fresh\n  2\nend\n"}},
	})
	if err != nil {
		t.Fatalf("marshal didChange: %v", err)
	}
	server.handleMessage(lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: payload})
	if got := server.documentLines(uri)[0]; got != "def fresh" {
		t.Fatalf("documentLines(%q)[0] after change = %q, want %q", uri, got, "def fresh")
	}
}

func BenchmarkLSPDefinitionLargeDocument(b *testing.B) {
	server, uri, callLine := benchmarkLSPNavigationServer(b)
	message := benchmarkPositionRequest(b, "textDocument/definition", uri, callLine, 4)

	b.ReportAllocs()
	for range b.N {
		messages := server.handleMessage(message)
		if len(messages) != 1 {
			b.Fatalf("definition responses = %d, want 1", len(messages))
		}
		if _, ok := messages[0].Result.(map[string]any); !ok {
			b.Fatalf("definition result = %#v, want location", messages[0].Result)
		}
	}
}

func BenchmarkLSPHoverLargeDocument(b *testing.B) {
	server, uri, callLine := benchmarkLSPNavigationServer(b)
	message := benchmarkPositionRequest(b, "textDocument/hover", uri, callLine, 4)

	b.ReportAllocs()
	for range b.N {
		messages := server.handleMessage(message)
		if len(messages) != 1 {
			b.Fatalf("hover responses = %d, want 1", len(messages))
		}
		if _, ok := messages[0].Result.(map[string]any); !ok {
			b.Fatalf("hover result = %#v, want contents", messages[0].Result)
		}
	}
}

func BenchmarkLSPDocumentSymbolLargeDocument(b *testing.B) {
	server, uri, _ := benchmarkLSPNavigationServer(b)
	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	if err != nil {
		b.Fatalf("marshal documentSymbol params: %v", err)
	}
	message := lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("103"),
		Method:  "textDocument/documentSymbol",
		Params:  payload,
	}

	b.ReportAllocs()
	for range b.N {
		messages := server.handleMessage(message)
		if len(messages) != 1 {
			b.Fatalf("documentSymbol responses = %d, want 1", len(messages))
		}
		symbols, ok := messages[0].Result.([]map[string]any)
		if !ok || len(symbols) != 2_001 {
			b.Fatalf("documentSymbol result = %#v, want 2001 symbols", messages[0].Result)
		}
	}
}

func benchmarkPositionRequest(b *testing.B, method, uri string, line, character int) lspInboundMessage {
	b.Helper()
	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": character},
	})
	if err != nil {
		b.Fatalf("marshal %s params: %v", method, err)
	}
	return lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("101"),
		Method:  method,
		Params:  payload,
	}
}

func benchmarkLSPNavigationServer(b *testing.B) (*lspServer, string, int) {
	b.Helper()
	server := newCompletionTestServer()
	uri := "file:///tmp/large.vibe"
	source, callLine := largeLSPNavigationSource(2_000)
	server.setDocument(uri, source)
	diagnostics := server.publishDiagnostics(uri, source).Params.(map[string]any)["diagnostics"].([]map[string]any)
	if len(diagnostics) != 0 {
		b.Fatalf("large navigation source diagnostics = %#v, want none", diagnostics)
	}
	return server, uri, callLine
}

func largeLSPNavigationSource(functionCount int) (string, int) {
	var b strings.Builder
	b.Grow(functionCount * 48)
	b.WriteString("def target(value)\n  value\nend\n\n")
	for i := range functionCount {
		b.WriteString("def caller_")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("\n  target(")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(")\nend\n\n")
	}
	callLine := 5 + 4*(functionCount-1)
	return b.String(), callLine
}
