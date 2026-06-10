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
	word := wordAtPosition(source, 1, 4)
	if word != "to_int" {
		t.Fatalf("expected to_int, got %q", word)
	}
}

func TestWordAtPositionUsesUTF16CharacterOffsets(t *testing.T) {
	t.Parallel()
	source := "😀😀x y\n"
	word := wordAtPosition(source, 0, 4)
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
		compiled: make(map[string]*vibes.Script),
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
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMemberContext(tc.source, tc.line, tc.chr); got != tc.want {
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
