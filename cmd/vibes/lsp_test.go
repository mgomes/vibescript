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
