package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgomes/vibescript/internal/ast"
	"github.com/mgomes/vibescript/vibes"
)

func TestRunCLIStartsLSPAndExitsOnEOF(t *testing.T) {
	t.Parallel()
	if err := runCLIContextWithIO(t.Context(), []string{"vibes", "lsp"}, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatalf("runCLI lsp failed: %v", err)
	}
}

func TestRunLSPContextCancelsBlockedRead(t *testing.T) {
	t.Parallel()
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		_ = writer.Close()
	})
	started := make(chan struct{})
	signalingReader := &signalingReadCloser{
		ReadCloser: reader,
		started:    started,
	}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		result <- runLSPContext(ctx, signalingReader, io.Discard)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("LSP did not begin reading")
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runLSPContext error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("LSP did not stop after cancellation")
	}
}

type signalingReadCloser struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (r *signalingReadCloser) Read(p []byte) (int, error) {
	r.once.Do(func() {
		close(r.started)
	})
	return r.ReadCloser.Read(p)
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

func TestDiagnosticsForSourceWithTopLevelScriptBody(t *testing.T) {
	t.Parallel()
	engine := vibes.MustNewEngine(vibes.Config{})
	source := "def double(x)\n  x * 2\nend\n\ndouble(3)\n"
	diags := diagnosticsForSource(engine, source)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %#v", diags)
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
	if first.Severity != 1 {
		t.Fatalf("expected severity 1, got %#v", first.Severity)
	}
	if first.Message == "" {
		t.Fatalf("expected non-empty diagnostic message, got %#v", first.Message)
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

	rng := diags[0].Range
	if rng.Start.Line != 0 || rng.Start.Character != 4 {
		t.Fatalf("start = %#v, want line 0 character 4", rng.Start)
	}
	if rng.End.Line != 0 || rng.End.Character != 7 {
		t.Fatalf("end = %#v, want line 0 character 7 (token span, not zero-width point)", rng.End)
	}
	if diags[0].Message != "expected function name, got integer" {
		t.Fatalf("message = %#v, want bare parser message", diags[0].Message)
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
		rng := diag.Range
		if rng.End.Line < rng.Start.Line || (rng.End.Line == rng.Start.Line && rng.End.Character <= rng.Start.Character) {
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

	rng := diags[0].Range
	if rng.Start.Line != 1 || rng.Start.Character != 17 {
		t.Fatalf("start = %#v, want line 1 character 17 (UTF-16 units)", rng.Start)
	}
	if rng.End.Line != 1 || rng.End.Character != 18 {
		t.Fatalf("end = %#v, want line 1 character 18 spanning the token", rng.End)
	}
}

func TestDiagnosticsForSourceWithoutPositionsReportDocumentStart(t *testing.T) {
	t.Parallel()
	engine := vibes.MustNewEngine(vibes.Config{})
	diags := diagnosticsForSource(engine, "def run()\n  1\nend\n\ndef run()\n  2\nend\n")
	if len(diags) != 1 {
		t.Fatalf("expected single positionless diagnostic, got %d", len(diags))
	}
	start := diags[0].Range.Start
	if start.Line != 0 || start.Character != 0 {
		t.Fatalf("positionless diagnostic start = %#v, want document start", start)
	}
	if diags[0].Message != "duplicate function run" {
		t.Fatalf("message = %#v, want compile error text", diags[0].Message)
	}
}

func TestCompletionItemsAreSortedAndCategorized(t *testing.T) {
	t.Parallel()
	items := testCompletionItems(t)
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
	if builtin["detail"] != testBuiltinSignature(t, "assert") {
		t.Fatalf("expected signature detail, got %#v", builtin["detail"])
	}
	if builtin["kind"] != 3 {
		t.Fatalf("expected builtin kind 3, got %#v", builtin["kind"])
	}

	// Namespace objects carry the namespace hover markdown and the
	// Module completion kind.
	namespace := findCompletionItem(t, items, "JSON")
	if namespace["detail"] != "namespace" {
		t.Fatalf("expected namespace detail, got %#v", namespace["detail"])
	}
	if namespace["kind"] != 9 {
		t.Fatalf("expected namespace kind 9, got %#v", namespace["kind"])
	}
}

func TestLSPKeywordCompletionsMatchParserKeywords(t *testing.T) {
	t.Parallel()
	items := testCompletionItems(t)
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
	require := findCompletionItem(t, items, "require")
	if require["detail"] != testBuiltinSignature(t, "require") {
		t.Fatalf("require detail = %#v, want its builtin signature", require["detail"])
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
	params2, ok := messages[0].Params.(lspPublishDiagnosticsParams)
	if !ok {
		t.Fatalf("unexpected params payload: %#v", messages[0].Params)
	}
	if params2.URI != "file:///tmp/test.vibe" {
		t.Fatalf("params uri = %q, want the opened document", params2.URI)
	}
	if len(params2.Diagnostics) == 0 {
		t.Fatalf("expected diagnostics for invalid source")
	}
}

// hoverValue drives a textDocument/hover request against a fresh
// server that opened source (so the navigation program used for
// user-symbol hover is cached) and returns the markdown contents value.
func hoverValue(t *testing.T, source string, line, character int) string {
	t.Helper()
	server := newCompletionTestServer()
	openDoc(t, server, "file:///tmp/test.vibe", source)
	return hoverValueAt(t, server, "file:///tmp/test.vibe", line, character)
}

// hoverValueAt drives a textDocument/hover request against server and
// returns the markdown contents value.
func hoverValueAt(t *testing.T, server *lspServer, uri string, line, character int) string {
	t.Helper()
	params := map[string]any{
		"textDocument": map[string]any{
			"uri": uri,
		},
		"position": map[string]any{
			"line":      line,
			"character": character,
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
	return value
}

func TestHandleMessageHoverServesBuiltinDocs(t *testing.T) {
	t.Parallel()
	value := hoverValue(t, "def run()\n  assert(true)\nend\n", 1, 3)
	if signature := "`" + testBuiltinSignature(t, "assert") + "`"; !strings.Contains(value, signature) {
		t.Fatalf("expected the assert signature in hover value, got %q", value)
	}
	if !strings.Contains(value, "Raises an error if `condition` is falsy") {
		t.Fatalf("expected the assert description in hover value, got %q", value)
	}
	if strings.Contains(value, "Vibescript builtin") {
		t.Fatalf("documented builtin must not fall back to the classifier line, got %q", value)
	}
}

func TestHandleMessageHoverResolvesQualifiedBuiltin(t *testing.T) {
	t.Parallel()
	source := "def run(raw)\n  JSON.parse_as(raw, { name: string })\nend\n"
	// Hover in the middle of parse_as: "  JSON.parse_as" puts the
	// member at characters 7-14.
	value := hoverValue(t, source, 1, 10)
	if !strings.Contains(value, "JSON.parse_as") {
		t.Fatalf("expected the qualified JSON.parse_as entry, got %q", value)
	}
	if !strings.Contains(value, "shape") {
		t.Fatalf("expected the parse_as description, got %q", value)
	}

	// The same member name without its namespace receiver has no doc
	// entry and degrades to the classifier line.
	bare := hoverValue(t, "def run(raw)\n  parse_as(raw)\nend\n", 1, 5)
	if !strings.Contains(bare, "Vibescript symbol") {
		t.Fatalf("bare member word should fall back to the classifier, got %q", bare)
	}
}

func TestHandleMessageHoverServesKeywordDocs(t *testing.T) {
	t.Parallel()
	value := hoverValue(t, "if true then\n  1\nend\n", 0, 9)
	if !strings.Contains(value, "`then`") {
		t.Fatalf("expected the keyword name in hover value, got %q", value)
	}
	if !strings.Contains(value, "Optional separator") {
		t.Fatalf("expected the then keyword doc, got %q", value)
	}
}

func TestHandleMessageHoverUnknownWordFallsBackToClassifier(t *testing.T) {
	t.Parallel()
	value := hoverValue(t, "def run()\n  frobnicate\nend\n", 1, 4)
	if value != "`frobnicate`\n\nVibescript symbol" {
		t.Fatalf("expected classifier fallback, got %q", value)
	}
}

func TestCompletionItemsCarryDocumentation(t *testing.T) {
	t.Parallel()
	items := testCompletionItems(t)

	builtin := findCompletionItem(t, items, "puts")
	doc, ok := builtin["documentation"].(map[string]any)
	if !ok {
		t.Fatalf("puts documentation = %#v, want markdown contents", builtin["documentation"])
	}
	if doc["kind"] != "markdown" {
		t.Fatalf("puts documentation kind = %#v, want markdown", doc["kind"])
	}
	if value, _ := doc["value"].(string); !strings.Contains(value, "Writes each value") {
		t.Fatalf("puts documentation value = %#v, want the builtins.md description", doc["value"])
	}
	if builtin["detail"] != testBuiltinSignature(t, "puts") {
		t.Fatalf("puts detail = %#v, want its signature", builtin["detail"])
	}

	keyword := findCompletionItem(t, items, "if")
	doc, ok = keyword["documentation"].(map[string]any)
	if !ok {
		t.Fatalf("if documentation = %#v, want markdown contents", keyword["documentation"])
	}
	if value, _ := doc["value"].(string); !strings.Contains(value, keywordDocs["if"]) {
		t.Fatalf("if documentation value = %#v, want the keyword doc", doc["value"])
	}
}

func TestParseBuiltinDocs(t *testing.T) {
	t.Parallel()
	markdown := "# Reference\n\n" +
		"## Formatting\n\n" +
		"### `format(pattern, *values)` / `sprintf(pattern, *values)`\n\n" +
		"Formats values with percent strings.\nSecond line of the paragraph.\n\n" +
		"```vibe\nformat(\"%d\", 1)\n# `fenced(code)` must not register entries\n```\n\n" +
		"Output is capped.\n\n" +
		"### Constants\n\n" +
		"- `Math::PI` – the circle constant.\n" +
		"- `Math.hypot(x, y)` / `Math.atan2(y, x)` – two-argument helpers\n" +
		"  spanning a continuation line.\n" +
		"- prose bullet with `inline(code)` that must not register.\n\n" +
		"### `Hash.new { |hash, key| ... }`\n\n" +
		"Builds a hash with a default proc.\n"
	entries := parseBuiltinDocs(markdown)

	format, ok := entries["format"]
	if !ok {
		t.Fatalf("entries = %v, want a format entry", entries)
	}
	if format.Signature != "`format(pattern, *values)`" {
		t.Fatalf("format signature = %q", format.Signature)
	}
	wantDoc := "`format(pattern, *values)` / `sprintf(pattern, *values)`\n\nFormats values with percent strings.\nSecond line of the paragraph.\n\nOutput is capped."
	if format.Markdown != wantDoc {
		t.Fatalf("format markdown = %q, want %q", format.Markdown, wantDoc)
	}
	if entries["sprintf"].Signature != "`sprintf(pattern, *values)`" {
		t.Fatalf("sprintf signature = %q", entries["sprintf"].Signature)
	}
	if entries["sprintf"].Markdown != format.Markdown {
		t.Fatalf("sprintf should share the format entry, got %q", entries["sprintf"].Markdown)
	}

	pi, ok := entries["Math.PI"]
	if !ok {
		t.Fatalf("entries = %v, want a Math.PI entry from the :: bullet", entries)
	}
	if pi.Markdown != "`Math::PI`\n\nthe circle constant." {
		t.Fatalf("Math.PI markdown = %q", pi.Markdown)
	}

	hypot, ok := entries["Math.hypot"]
	if !ok {
		t.Fatal("want a Math.hypot entry from the multi-span bullet")
	}
	if !strings.Contains(hypot.Markdown, "spanning a continuation line.") {
		t.Fatalf("Math.hypot markdown lost its continuation line: %q", hypot.Markdown)
	}
	if entries["Math.atan2"].Markdown != hypot.Markdown {
		t.Fatal("Math.atan2 should share the bullet entry")
	}

	newHash, ok := entries["Hash.new"]
	if !ok {
		t.Fatal("want a Hash.new entry")
	}
	if newHash.Signature != "`Hash.new { |hash, key| ... }`" {
		t.Fatalf("Hash.new signature = %q", newHash.Signature)
	}

	for name := range entries {
		switch name {
		case "format", "sprintf", "Math.PI", "Math.hypot", "Math.atan2", "Hash.new":
		default:
			t.Errorf("unexpected entry %q (fenced or prose code spans must not register)", name)
		}
	}
}

// TestBuiltinDocsMatchRegisteredBuiltins is the documentation drift gate:
// every non-namespace global and namespace member must be documented, and the
// docs cannot retain entries for builtins the runtime no longer registers.
func TestBuiltinDocsMatchRegisteredBuiltins(t *testing.T) {
	t.Parallel()
	docs := builtinDocs()
	if len(docs) == 0 {
		t.Fatal("no builtin documentation entries were parsed from docs/builtins.md")
	}

	catalog := newTestBuiltinCatalog(t)
	registered := make(map[string]struct{}, len(catalog.documentedNames))
	var missing []string
	for _, name := range catalog.documentedNames {
		registered[name] = struct{}{}
		if _, ok := docs[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("registered builtins missing docs/builtins.md entries: %v", missing)
	}

	var stale []string
	for name := range docs {
		if _, ok := registered[name]; !ok {
			stale = append(stale, name)
		}
	}
	slices.Sort(stale)
	if len(stale) > 0 {
		t.Errorf("docs/builtins.md entries missing registered builtins: %v", stale)
	}
}

// TestKeywordDocsCoverParserKeywords gates the keyword table the same
// way: every reserved keyword and contextual word has a description,
// and the table holds nothing else.
func TestKeywordDocsCoverParserKeywords(t *testing.T) {
	t.Parallel()
	known := make(map[string]struct{}, len(ast.Keywords())+len(lspContextualWords))
	for _, keyword := range ast.Keywords() {
		known[keyword] = struct{}{}
		if doc, ok := keywordDocs[keyword]; !ok {
			t.Errorf("reserved keyword %q has no keywordDocs entry", keyword)
		} else if len(doc) == 0 || len(doc) > 130 {
			t.Errorf("keywordDocs[%q] length %d, want a short one-liner", keyword, len(doc))
		}
	}
	for _, word := range lspContextualWords {
		known[word] = struct{}{}
		if _, ok := keywordDocs[word]; !ok {
			t.Errorf("contextual word %q has no keywordDocs entry", word)
		}
	}
	for name := range keywordDocs {
		if _, ok := known[name]; !ok {
			t.Errorf("keywordDocs entry %q is neither a reserved keyword nor a contextual word", name)
		}
	}
}

func TestQualifiedWordAt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		line      string
		character int
		want      string
	}{
		{name: "namespace member", line: "JSON.parse_as(raw)", character: 8, want: "JSON.parse_as"},
		{name: "bare word", line: "parse_as(raw)", character: 3, want: ""},
		{name: "space before member", line: "JSON. parse_as", character: 8, want: ""},
		{name: "dot without receiver", line: ".parse_as", character: 3, want: ""},
		{name: "chained receiver word", line: "payload.keys.sort", character: 9, want: "payload.keys"},
		{name: "scope resolution member", line: "Math::PI", character: 7, want: "Math.PI"},
		{name: "chained namespace segment does not qualify", line: "payload.JSON.parse", character: 14, want: ""},
		{name: "chained scope segment does not qualify", line: "A::JSON.parse", character: 9, want: ""},
		{name: "single colon is not an accessor", line: "label: value", character: 8, want: ""},
		{name: "scope resolution without receiver", line: "::PI", character: 3, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := qualifiedWordAt([]string{tt.line}, 0, tt.character)
			if got != tt.want {
				t.Fatalf("qualifiedWordAt(%q, %d) = %q, want %q", tt.line, tt.character, got, tt.want)
			}
		})
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
		engine:      vibes.MustNewEngine(vibes.Config{}),
		docs:        make(map[string]string),
		lines:       make(map[string][]string),
		compiled:    make(map[string]*vibes.Script),
		completions: make(map[string]*lspCompletionIndex),
		programs:    make(map[string]*ast.Program),
		published:   make(map[string]publishedDiagnostics),
		symbols:     make(map[string][]lspDocumentSymbol),
	}
}

func TestCompletionIndexBuiltLazily(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/lazy.vibe"
	openDoc(t, server, uri, "def helper(value)\n  value\nend\n\ndef run()\n  helper(1)\nend\n")

	// The didOpen publish compiles the document but must not eagerly
	// build the completion index, which is the editor hot path.
	if _, ok := server.completions[uri]; ok {
		t.Fatal("didOpen eagerly built the completion index")
	}

	labels := completionLabels(t, server, uri, 5, 2)
	if _, ok := labels["helper"]; !ok {
		t.Fatalf("lazy completion index missing user-defined function helper: %d items", len(labels))
	}
	if _, ok := server.completions[uri]; !ok {
		t.Fatal("completion request did not cache the lazily built index")
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
	// A string receiver must not be offered array members: completion is
	// narrowed to the receiver's kind when that kind is known from syntax.
	if _, hasArray := labels["flatten"]; hasArray {
		t.Fatal("member completion offered the array method flatten on a string receiver")
	}
	if _, hasMoney := labels["cents"]; hasMoney {
		t.Fatal("member completion offered the money method cents on a string receiver")
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
		"assert":  testBuiltinSignature(t, "assert"),
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

func TestCompletionOffersDestructuredLocals(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/destructured-locals.vibe"
	openDoc(t, server, uri, `def run()
  first, *rest, last = [1, 2, 3]
  nested, (left, right) = [4, [5, 6]]
  first
end
`)

	labels := completionLabels(t, server, uri, 3, 2)
	for _, want := range []string{"first", "rest", "last", "nested", "left", "right"} {
		if _, ok := labels[want]; !ok {
			t.Fatalf("completion missing destructured local %q", want)
		}
	}
}

func TestCompletionOffersLocalsFromBeginElse(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/begin-else-locals.vibe"
	openDoc(t, server, uri, `def run()
  begin
    value = 1
  rescue
    fallback = 2
  else
    from_else = value
  end
  from_else
end
`)

	labels := completionLabels(t, server, uri, 8, 2)
	for _, want := range []string{"value", "fallback", "from_else"} {
		if _, ok := labels[want]; !ok {
			t.Fatalf("completion missing local %q", want)
		}
	}
}

func TestCompletionOffersLocalsFromContinuedStatementExpressions(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/continued-statement-expression-locals.vibe"
	openDoc(t, server, uri, `def run(flag)
  if flag; x = 1; end.to_s
  x
end
`)

	labels := completionLabels(t, server, uri, 2, 2)
	if _, ok := labels["x"]; !ok {
		t.Fatal("completion missing local from continued if statement expression")
	}
}

func TestCompletionOffersRescueBindingOnlyInsideHandler(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/rescue-binding-locals.vibe"
	openDoc(t, server, uri, `def run()
  begin
    raise("boom")
  rescue RuntimeError => err
    err.message
  end
  nil
end
`)

	inside := completionLabels(t, server, uri, 4, 4)
	if _, ok := inside["err"]; !ok {
		t.Fatal("rescue binding missing inside handler")
	}

	after := completionLabels(t, server, uri, 6, 2)
	if _, leaked := after["err"]; leaked {
		t.Fatal("rescue binding leaked after handler")
	}
}

func TestCompletionOffersNestedRescueBindings(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/nested-rescue-binding-locals.vibe"
	openDoc(t, server, uri, `def run()
  begin
    raise("outer")
  rescue RuntimeError => outer_err
    begin
      raise("inner")
    rescue RuntimeError => inner_err
      inner_err
    end
    outer_err
  end
end
`)

	insideInner := completionLabels(t, server, uri, 7, 6)
	for _, want := range []string{"outer_err", "inner_err"} {
		if _, ok := insideInner[want]; !ok {
			t.Fatalf("nested rescue completion missing %q", want)
		}
	}

	afterInner := completionLabels(t, server, uri, 9, 4)
	if _, ok := afterInner["outer_err"]; !ok {
		t.Fatal("outer rescue binding missing after nested handler")
	}
	if _, leaked := afterInner["inner_err"]; leaked {
		t.Fatal("inner rescue binding leaked after nested handler")
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
		{name: "float_exponent", source: "1.5e2", line: 0, chr: 5, want: false},
		{name: "float_exponent_upper", source: "1.5E6", line: 0, chr: 5, want: false},
		{name: "float_exponent_underscore", source: "1.5e1_0", line: 0, chr: 7, want: false},
		{name: "float_exponent_in_progress", source: "1.5e", line: 0, chr: 4, want: false},
		{name: "member_on_exponent_float", source: "1.5e2.foo", line: 0, chr: 9, want: true},
		{name: "float_with_trailing_letter", source: "1.5x", line: 0, chr: 4, want: true},
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

func TestSignatureHelpForOptionalKeywordParameter(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/sigkw.vibe"
	openDoc(t, server, uri, `def configure(host:, port: 8080, scheme: "https")
  host
end

def run()
  configure(host: "a")
end
`)

	result, ok := signatureHelpResult(t, server, uri, 5, 18).(map[string]any)
	if !ok {
		t.Fatal("expected signature help for optional keyword function")
	}
	signatures := result["signatures"].([]map[string]any)
	if len(signatures) != 1 {
		t.Fatalf("expected one signature, got %d", len(signatures))
	}
	label := signatures[0]["label"].(string)
	// The required keyword renders as `host:`, while the optional keyword-only
	// parameters render their default after the colon (`port: …`), not with the
	// positional `= …` marker.
	if !strings.Contains(label, "configure(host:, port: …, scheme: …)") {
		t.Fatalf("label = %q, want optional keyword defaults rendered after the colon", label)
	}
}

func TestParamLabelOptionalKeyword(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		param ast.Param
		want  string
	}{
		{
			name:  "required_keyword",
			param: ast.Param{Name: "host", Kind: ast.ParamKeyword},
			want:  "host:",
		},
		{
			name:  "optional_keyword_default",
			param: ast.Param{Name: "port", Kind: ast.ParamKeyword, DefaultVal: &ast.IntegerLiteral{Value: 8080}},
			want:  "port: …",
		},
		{
			name:  "positional_default",
			param: ast.Param{Name: "count", DefaultVal: &ast.IntegerLiteral{Value: 1}},
			want:  "count = …",
		},
		{
			name:  "typed_positional",
			param: ast.Param{Name: "amount", Type: &ast.TypeExpr{Name: "int", Kind: ast.TypeInt}},
			want:  "amount: int",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := paramLabel(tt.param); got != tt.want {
				t.Fatalf("paramLabel(%+v) = %q, want %q", tt.param, got, tt.want)
			}
		})
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
	catalog := newTestBuiltinCatalog(t)
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
		{name: "namespace_call", source: "JSON.parse(", line: 0, chr: 11, callee: "JSON.parse", param: 0, ok: true},
		{name: "member_call_suppressed", source: "price.format(", line: 0, chr: 13, ok: false},
		{name: "comment_suppressed", source: "# money_cents(", line: 0, chr: 14, ok: false},
		{name: "space_before_paren", source: "charge (100, ", line: 0, chr: 13, callee: "charge", param: 1, ok: true},
		{name: "hash_in_string_not_comment", source: `charge("#", `, line: 0, chr: 12, callee: "charge", param: 1, ok: true},
		{name: "hash_in_single_string_not_comment", source: `charge('#', `, line: 0, chr: 12, callee: "charge", param: 1, ok: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			callee, param, ok := enclosingCall(catalog, splitLSPLines(tc.source), tc.line, tc.chr)
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

func TestBuiltinCatalogDrivesLSPCompletionsAndSignatures(t *testing.T) {
	t.Parallel()
	catalog := newTestBuiltinCatalog(t)
	items := buildCompletionItems(catalog)
	completed := make(map[string]struct{}, len(items))
	for _, item := range items {
		name, ok := item["label"].(string)
		if ok {
			completed[name] = struct{}{}
		}
	}

	var missing []string
	for _, name := range catalog.topLevelNames {
		if _, ok := completed[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("registered builtins missing LSP completions: %v", missing)
	}

	var missingSignatures []string
	for _, name := range catalog.functionNames {
		entry, ok := builtinDocs()[name]
		if !ok || entry.Signature == "" {
			missingSignatures = append(missingSignatures, name)
		}
	}
	if len(missingSignatures) > 0 {
		t.Errorf("registered builtin functions missing documented signatures: %v", missingSignatures)
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
	symbols, ok := messages[0].Result.([]lspDocumentSymbol)
	if !ok {
		t.Fatalf("expected symbol list, got %#v", messages[0].Result)
	}
	byName := map[string]lspDocumentSymbol{}
	for _, symbol := range symbols {
		byName[symbol.Name] = symbol
	}
	if len(symbols) != 4 {
		t.Fatalf("expected 4 top-level symbols, got %d", len(symbols))
	}
	if byName["helper"].Kind != 12 || byName["run"].Kind != 12 {
		t.Fatal("functions should have kind 12")
	}
	wallet := byName["Wallet"]
	if wallet.Kind != 5 {
		t.Fatalf("Wallet kind = %d, want class kind 5", wallet.Kind)
	}
	childNames := make([]string, 0, len(wallet.Children))
	for _, child := range wallet.Children {
		childNames = append(childNames, child.Name)
	}
	if !slices.Contains(childNames, "balance") || !slices.Contains(childNames, "self.empty") {
		t.Fatalf("Wallet children = %v, want balance and self.empty", childNames)
	}
	status := byName["Status"]
	if status.Kind != 10 {
		t.Fatalf("Status kind = %d, want enum kind 10", status.Kind)
	}
	if members := status.Children; len(members) != 2 {
		t.Fatalf("Status members = %d, want 2", len(members))
	}
}

func TestDocumentSymbolsWireShape(t *testing.T) {
	t.Parallel()
	lines := splitLSPLines("class Wallet\n  def balance()\n    1\n  end\nend\n")
	child := symbolFor("balance", 6, 1, lines, nil)
	parent := symbolFor("Wallet", 5, 0, lines, []lspDocumentSymbol{child})

	leafJSON, err := json.Marshal(child)
	if err != nil {
		t.Fatalf("marshal leaf symbol: %v", err)
	}
	// A leaf symbol omits the optional children field, matching the LSP
	// DocumentSymbol shape clients expect for a method or member.
	if strings.Contains(string(leafJSON), "children") {
		t.Fatalf("leaf symbol JSON = %s, want no children field", leafJSON)
	}

	parentJSON, err := json.Marshal(parent)
	if err != nil {
		t.Fatalf("marshal parent symbol: %v", err)
	}
	want := `{"name":"Wallet","kind":5,` +
		`"range":{"start":{"line":0,"character":0},"end":{"line":1,"character":15}},` +
		`"selectionRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":12}},` +
		`"children":[{"name":"balance","kind":6,` +
		`"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":15}},` +
		`"selectionRange":{"start":{"line":1,"character":0},"end":{"line":1,"character":15}}}]}`
	if string(parentJSON) != want {
		t.Fatalf("parent symbol JSON =\n%s\nwant\n%s", parentJSON, want)
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

func TestPublishDiagnosticsClearsNavigationWhenParsingIsSkipped(t *testing.T) {
	t.Parallel()
	server := &lspServer{
		engine:      vibes.MustNewEngine(vibes.Config{MaxSourceBytes: 64}),
		docs:        make(map[string]string),
		lines:       make(map[string][]string),
		compiled:    make(map[string]*vibes.Script),
		completions: make(map[string]*lspCompletionIndex),
		programs:    make(map[string]*ast.Program),
	}
	uri := "file:///tmp/too-large.vibe"
	source := "def old\n  1\nend\n"
	server.setDocument(uri, source)
	diagnostics := server.publishDiagnostics(uri, source).Params.(lspPublishDiagnosticsParams).Diagnostics
	if len(diagnostics) != 0 {
		t.Fatalf("initial diagnostics = %#v, want none", diagnostics)
	}
	if server.programs[uri] == nil {
		t.Fatal("initial publish did not cache navigation program")
	}
	if server.compiled[uri] == nil {
		t.Fatal("initial publish did not cache compiled script")
	}
	// The completion index is built lazily, so the diagnostics-only
	// publish must not eagerly populate it.
	if _, ok := server.completions[uri]; ok {
		t.Fatal("diagnostics publish eagerly built the completion index")
	}
	if server.completionIndex(uri) == nil {
		t.Fatal("completionIndex did not build from the compiled script")
	}
	if _, ok := server.completions[uri]; !ok {
		t.Fatal("completionIndex did not cache the built index")
	}

	oversized := strings.Repeat(source, 8)
	server.setDocument(uri, oversized)
	diagnostics = server.publishDiagnostics(uri, oversized).Params.(lspPublishDiagnosticsParams).Diagnostics
	if len(diagnostics) == 0 {
		t.Fatal("oversized publish diagnostics = none, want source-size diagnostic")
	}
	if _, ok := server.programs[uri]; ok {
		t.Fatal("oversized publish kept stale navigation program")
	}
	if _, ok := server.compiled[uri]; ok {
		t.Fatal("oversized publish kept stale compiled script")
	}
	if _, ok := server.completions[uri]; ok {
		t.Fatal("oversized publish kept stale completion index")
	}
	if server.completionIndex(uri) != nil {
		t.Fatal("completionIndex returned a stale index after the compiled script was dropped")
	}
}

func TestPublishDiagnosticsSkipsRecompileForUnchangedSource(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/unchanged.vibe"
	source := "def helper(n)\n  n\nend\n"
	server.setDocument(uri, source)
	if diags := server.publishDiagnostics(uri, source).Params.(lspPublishDiagnosticsParams).Diagnostics; len(diags) != 0 {
		t.Fatalf("initial diagnostics = %#v, want none", diags)
	}
	program := server.programs[uri]
	if program == nil {
		t.Fatal("initial publish did not cache navigation program")
	}

	// Republishing byte-identical text (open/save replays, reverted
	// buffers) must reuse the previous result instead of recompiling.
	server.setDocument(uri, source)
	if diags := server.publishDiagnostics(uri, source).Params.(lspPublishDiagnosticsParams).Diagnostics; len(diags) != 0 {
		t.Fatalf("republished diagnostics = %#v, want none", diags)
	}
	if server.programs[uri] != program {
		t.Fatal("unchanged republish recompiled the document")
	}

	edited := source + "def broken(\n"
	server.setDocument(uri, edited)
	if diags := server.publishDiagnostics(uri, edited).Params.(lspPublishDiagnosticsParams).Diagnostics; len(diags) == 0 {
		t.Fatal("edited publish diagnostics = none, want parse errors")
	}
	editedProgram := server.programs[uri]

	// Reverting to the original text misses the cache (the last compile
	// saw the edited text) and must produce a fresh clean result.
	server.setDocument(uri, source)
	if diags := server.publishDiagnostics(uri, source).Params.(lspPublishDiagnosticsParams).Diagnostics; len(diags) != 0 {
		t.Fatalf("reverted diagnostics = %#v, want none", diags)
	}
	if server.programs[uri] == editedProgram {
		t.Fatal("reverted publish did not recompile after an intervening edit")
	}
}

func TestPublishDiagnosticsWireFormat(t *testing.T) {
	t.Parallel()
	message := diagnosticsNotification("file:///tmp/wire.vibe", []lspDiagnostic{
		newDiagnostic(diagnosticRange{startLine: 1, startChar: 2, endLine: 1, endChar: 5}, "boom"),
	})
	payload, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	var decoded struct {
		Params struct {
			URI         string           `json:"uri"`
			Diagnostics []map[string]any `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal notification: %v", err)
	}
	if decoded.Params.URI != "file:///tmp/wire.vibe" {
		t.Fatalf("uri = %q, want the published document", decoded.Params.URI)
	}
	if len(decoded.Params.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want one entry", decoded.Params.Diagnostics)
	}
	diag := decoded.Params.Diagnostics[0]
	if diag["severity"] != float64(1) || diag["source"] != "vibes-lsp" || diag["message"] != "boom" {
		t.Fatalf("diagnostic fields = %#v, want severity/source/message wire keys", diag)
	}
	rng, ok := diag["range"].(map[string]any)
	if !ok {
		t.Fatalf("diagnostic range = %#v, want nested start/end object", diag["range"])
	}
	start, ok := rng["start"].(map[string]any)
	if !ok || start["line"] != float64(1) || start["character"] != float64(2) {
		t.Fatalf("range start = %#v, want line/character wire keys", rng["start"])
	}
	end, ok := rng["end"].(map[string]any)
	if !ok || end["line"] != float64(1) || end["character"] != float64(5) {
		t.Fatalf("range end = %#v, want line/character wire keys", rng["end"])
	}

	clean, err := json.Marshal(diagnosticsNotification("file:///tmp/wire.vibe", []lspDiagnostic{}))
	if err != nil {
		t.Fatalf("marshal clean notification: %v", err)
	}
	if !strings.Contains(string(clean), `"diagnostics":[]`) {
		t.Fatalf("clean notification %s, want an empty diagnostics array, not null", clean)
	}
}

func documentSymbolsResult(t *testing.T, server *lspServer, uri string) []lspDocumentSymbol {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	if err != nil {
		t.Fatalf("marshal documentSymbol params: %v", err)
	}
	messages := server.handleMessage(lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("31"),
		Method:  "textDocument/documentSymbol",
		Params:  payload,
	})
	if len(messages) != 1 {
		t.Fatalf("documentSymbol responses = %d, want 1", len(messages))
	}
	symbols, ok := messages[0].Result.([]lspDocumentSymbol)
	if !ok {
		t.Fatalf("documentSymbol result = %#v, want []lspDocumentSymbol", messages[0].Result)
	}
	return symbols
}

func TestDocumentSymbolOutlineCachedBetweenEdits(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/outline-cache.vibe"
	openDoc(t, server, uri, "def alpha()\n  1\nend\n\ndef beta()\n  2\nend\n")

	first := documentSymbolsResult(t, server, uri)
	if len(first) != 2 {
		t.Fatalf("outline = %d symbols, want 2", len(first))
	}
	second := documentSymbolsResult(t, server, uri)
	if &first[0] != &second[0] {
		t.Fatal("repeat outline request rebuilt the symbols instead of reusing the cache")
	}

	// Shift the declarations down while leaving the buffer unparsable:
	// the cached outline predates the edit, so it must be dropped and
	// the fresh outline anchored to the live buffer lines.
	payload, err := json.Marshal(map[string]any{
		"textDocument":   map[string]any{"uri": uri},
		"contentChanges": []map[string]any{{"text": "# one\n# two\ndef alpha()\n  1\nend\n\ndef beta()\n  2\nend\n\ndef broken("}},
	})
	if err != nil {
		t.Fatalf("marshal didChange: %v", err)
	}
	server.handleMessage(lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: payload})

	shifted := documentSymbolsResult(t, server, uri)
	if len(shifted) != 2 {
		t.Fatalf("outline after mid-edit parse = %d symbols, want 2", len(shifted))
	}
	if shifted[0].Range.Start.Line != 2 {
		t.Fatalf("alpha outline line = %d, want 2 after the buffer shifted", shifted[0].Range.Start.Line)
	}

	// Replace the buffer with text declaring none of the symbols: the
	// retained navigation program must not resurrect the old outline.
	payload, err = json.Marshal(map[string]any{
		"textDocument":   map[string]any{"uri": uri},
		"contentChanges": []map[string]any{{"text": "def broken("}},
	})
	if err != nil {
		t.Fatalf("marshal didChange: %v", err)
	}
	server.handleMessage(lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: payload})
	if emptied := documentSymbolsResult(t, server, uri); len(emptied) != 0 {
		t.Fatalf("outline = %d symbols for a buffer containing none of them", len(emptied))
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
	if want := testBuiltinSignature(t, "assert"); label != want {
		t.Fatalf("label = %q, want %q", label, want)
	}
	if result["activeParameter"] != 1 {
		t.Fatalf("activeParameter = %#v, want 1 after the comma", result["activeParameter"])
	}
}

func TestSignatureHelpForQualifiedBuiltin(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/qualified-signature.vibe"
	source := "def run()\n  JSON.parse_as(\"{}\", { name: string })\nend\n"
	openDoc(t, server, uri, source)
	line := splitLSPLines(source)[1]
	cursor := strings.Index(line, "{ name")

	result, ok := signatureHelpResult(t, server, uri, 1, cursor).(map[string]any)
	if !ok {
		t.Fatal("expected signature help for JSON.parse_as")
	}
	label := result["signatures"].([]map[string]any)[0]["label"].(string)
	if want := testBuiltinSignature(t, "JSON.parse_as"); label != want {
		t.Fatalf("label = %q, want %q", label, want)
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
		if len(symbol.Children) == 0 {
			continue
		}
		parentEnd := symbol.Range.End.Line
		for _, child := range symbol.Children {
			childEnd := child.Range.End.Line
			if childEnd > parentEnd {
				t.Fatalf("%s child %s ends at line %d outside parent end %d",
					symbol.Name, child.Name, childEnd, parentEnd)
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

func BenchmarkLSPPublishDiagnosticsLargeDocument(b *testing.B) {
	server := newCompletionTestServer()
	uri := "file:///tmp/diagnostics-large.vibe"
	source, _ := largeLSPNavigationSource(2_000)
	server.setDocument(uri, source)

	b.ReportAllocs()
	for range b.N {
		message := server.publishDiagnostics(uri, source)
		diagnostics := message.Params.(lspPublishDiagnosticsParams).Diagnostics
		if len(diagnostics) != 0 {
			b.Fatalf("publishDiagnostics diagnostics = %#v, want none", diagnostics)
		}
	}
}

// BenchmarkLSPPublishDiagnosticsLargeDocumentCacheMiss alternates two sources
// so every publish recompiles, keeping the compile-path cost measurable now
// that identical-text republishes are served from the per-URI cache.
func BenchmarkLSPPublishDiagnosticsLargeDocumentCacheMiss(b *testing.B) {
	server := newCompletionTestServer()
	uri := "file:///tmp/diagnostics-large-miss.vibe"
	source, _ := largeLSPNavigationSource(2_000)
	sources := [2]string{source, source + "\ndef cache_miss_extra()\n  1\nend\n"}

	b.ReportAllocs()
	for i := range b.N {
		text := sources[i%2]
		server.setDocument(uri, text)
		message := server.publishDiagnostics(uri, text)
		diagnostics := message.Params.(lspPublishDiagnosticsParams).Diagnostics
		if len(diagnostics) != 0 {
			b.Fatalf("publishDiagnostics diagnostics = %#v, want none", diagnostics)
		}
	}
}

func BenchmarkLSPCompletionLargeDocument(b *testing.B) {
	server := newCompletionTestServer()
	uri := "file:///tmp/completion-large.vibe"
	source, completionLine := largeLSPCompletionSource(2_000, 8)
	server.setDocument(uri, source)
	diagnostics := server.publishDiagnostics(uri, source).Params.(lspPublishDiagnosticsParams).Diagnostics
	if len(diagnostics) != 0 {
		b.Fatalf("large completion source diagnostics = %#v, want none", diagnostics)
	}
	message := benchmarkPositionRequest(b, "textDocument/completion", uri, completionLine, 2)

	b.ReportAllocs()
	for range b.N {
		messages := server.handleMessage(message)
		if len(messages) != 1 {
			b.Fatalf("completion responses = %d, want 1", len(messages))
		}
		result := messages[0].Result.(map[string]any)
		items := result["items"].([]map[string]any)
		if len(items) < 2_000 {
			b.Fatalf("completion items = %d, want at least function completions", len(items))
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
		symbols, ok := messages[0].Result.([]lspDocumentSymbol)
		if !ok || len(symbols) != 2_001 {
			b.Fatalf("documentSymbol result = %#v, want 2001 symbols", messages[0].Result)
		}
	}
}

// BenchmarkLSPDocumentSymbolLargeDocumentCacheMiss re-sets the document each
// iteration so the outline cache never serves the request, keeping the
// outline-render cost measurable.
func BenchmarkLSPDocumentSymbolLargeDocumentCacheMiss(b *testing.B) {
	server := newCompletionTestServer()
	uri := "file:///tmp/symbols-large-miss.vibe"
	source, _ := largeLSPNavigationSource(2_000)
	server.setDocument(uri, source)
	diagnostics := server.publishDiagnostics(uri, source).Params.(lspPublishDiagnosticsParams).Diagnostics
	if len(diagnostics) != 0 {
		b.Fatalf("large symbol source diagnostics = %#v, want none", diagnostics)
	}
	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	if err != nil {
		b.Fatalf("marshal documentSymbol params: %v", err)
	}
	message := lspInboundMessage{
		JSONRPC: "2.0",
		ID:      rawID("104"),
		Method:  "textDocument/documentSymbol",
		Params:  payload,
	}

	b.ReportAllocs()
	for range b.N {
		server.setDocument(uri, source)
		messages := server.handleMessage(message)
		if len(messages) != 1 {
			b.Fatalf("documentSymbol responses = %d, want 1", len(messages))
		}
		symbols, ok := messages[0].Result.([]lspDocumentSymbol)
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
	diagnostics := server.publishDiagnostics(uri, source).Params.(lspPublishDiagnosticsParams).Diagnostics
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

func largeLSPCompletionSource(functionCount, localCount int) (string, int) {
	var b strings.Builder
	b.Grow(functionCount * (40 + localCount*24))
	line := 0
	completionLine := 0
	for i := range functionCount {
		b.WriteString("def caller_")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("(arg)\n")
		line++
		for j := range localCount {
			if i == functionCount-1 && j == localCount-1 {
				completionLine = line
			}
			b.WriteString("  local_")
			b.WriteString(strconv.Itoa(i))
			b.WriteString("_")
			b.WriteString(strconv.Itoa(j))
			b.WriteString(" = arg\n")
			line++
		}
		b.WriteString("  local_")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("_")
		b.WriteString(strconv.Itoa(localCount - 1))
		b.WriteString("\nend\n\n")
		line += 3
	}
	return b.String(), completionLine
}

const moduleNavigationFixture = `module Billing
  LIMIT = 100

  module Codes
    PREFIX = "B"

    def self.tag
      PREFIX
    end
  end

  def self.code
    "B-1"
  end
end

class Account
  protected def guard
    1
  end

  public def shown
    2
  end
end

def run()
  Billing::LIMIT
end
`

func TestDocumentSymbolsIncludeModulesAndVisibilityPrefixedDefs(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/outline-modules.vibe"
	openDoc(t, server, uri, moduleNavigationFixture)

	symbols := documentSymbolsResult(t, server, uri)
	byName := map[string]lspDocumentSymbol{}
	for _, symbol := range symbols {
		byName[symbol.Name] = symbol
	}

	billing, ok := byName["Billing"]
	if !ok {
		t.Fatalf("outline %v is missing the Billing module", symbolNames(symbols))
	}
	if billing.Kind != 2 {
		t.Fatalf("Billing kind = %d, want module kind 2", billing.Kind)
	}
	billingChildren := map[string]lspDocumentSymbol{}
	for _, child := range billing.Children {
		billingChildren[child.Name] = child
	}
	if limit, ok := billingChildren["LIMIT"]; !ok || limit.Kind != 14 {
		t.Fatalf("Billing children = %v, want constant LIMIT with kind 14", symbolNames(billing.Children))
	}
	if code, ok := billingChildren["self.code"]; !ok || code.Kind != 6 {
		t.Fatalf("Billing children = %v, want method self.code with kind 6", symbolNames(billing.Children))
	}
	codes, ok := billingChildren["Codes"]
	if !ok || codes.Kind != 2 {
		t.Fatalf("Billing children = %v, want nested module Codes with kind 2", symbolNames(billing.Children))
	}
	codesChildren := map[string]lspDocumentSymbol{}
	for _, child := range codes.Children {
		codesChildren[child.Name] = child
	}
	if tag, ok := codesChildren["self.tag"]; !ok || tag.Kind != 6 {
		t.Fatalf("Codes children = %v, want method self.tag", symbolNames(codes.Children))
	}
	if prefix, ok := codesChildren["PREFIX"]; !ok || prefix.Kind != 14 {
		t.Fatalf("Codes children = %v, want constant PREFIX", symbolNames(codes.Children))
	}

	account := byName["Account"]
	if account.Kind != 5 {
		t.Fatalf("Account kind = %d, want class kind 5", account.Kind)
	}
	accountChildren := make([]string, 0, len(account.Children))
	for _, child := range account.Children {
		accountChildren = append(accountChildren, child.Name)
	}
	if !slices.Contains(accountChildren, "guard") || !slices.Contains(accountChildren, "shown") {
		t.Fatalf("Account children = %v, want visibility-prefixed defs guard and shown", accountChildren)
	}
}

func symbolNames(symbols []lspDocumentSymbol) []string {
	names := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		names = append(names, symbol.Name)
	}
	return names
}

func TestDocumentSymbolModuleOutlineServedFromCache(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/outline-modules-cache.vibe"
	openDoc(t, server, uri, moduleNavigationFixture)

	first := documentSymbolsResult(t, server, uri)
	if len(first) != 3 {
		t.Fatalf("outline = %d top-level symbols, want Billing, Account, run", len(first))
	}
	second := documentSymbolsResult(t, server, uri)
	if &first[0] != &second[0] {
		t.Fatal("repeat outline request rebuilt the symbols instead of reusing the cache")
	}
	if second[0].Name != "Billing" || second[0].Kind != 2 {
		t.Fatalf("cached outline root = %s kind %d, want Billing kind 2", second[0].Name, second[0].Kind)
	}
}

func TestDefinitionResolvesModulesAndVisibilityPrefixedDefs(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/nav-modules.vibe"
	openDoc(t, server, uri, moduleNavigationFixture)

	wantLines := map[string]int{
		"Billing": 0,
		"LIMIT":   1,
		"Codes":   3,
		"PREFIX":  4,
		"tag":     6,
		"code":    11,
		"guard":   17,
		"shown":   21,
	}
	for word, wantLine := range wantLines {
		location := definitionLocation(server.programs[uri], uri, server.documentLines(uri), word)
		if location == nil {
			t.Fatalf("definition for %s = nil, want line %d", word, wantLine)
		}
		start := location["range"].(map[string]any)["start"].(map[string]any)
		if start["line"] != wantLine {
			t.Fatalf("definition line for %s = %#v, want %d", word, start["line"], wantLine)
		}
	}
}

func closeDoc(t *testing.T, server *lspServer, uri string) []lspOutboundMessage {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	if err != nil {
		t.Fatalf("marshal didClose: %v", err)
	}
	return server.handleMessage(lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didClose", Params: payload})
}

func openDocDiagnostics(t *testing.T, server *lspServer, uri, text string) []lspDiagnostic {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": text},
	})
	if err != nil {
		t.Fatalf("marshal didOpen: %v", err)
	}
	messages := server.handleMessage(lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: payload})
	if len(messages) != 1 {
		t.Fatalf("didOpen responses = %d, want one publishDiagnostics notification", len(messages))
	}
	params, ok := messages[0].Params.(lspPublishDiagnosticsParams)
	if !ok {
		t.Fatalf("didOpen params = %#v, want lspPublishDiagnosticsParams", messages[0].Params)
	}
	return params.Diagnostics
}

// perURIMaps returns every string-keyed map field on lspServer by name,
// so the eviction test fails automatically when a new per-document map
// is added without being wired into evictDocument.
func perURIMaps(server *lspServer) map[string]reflect.Value {
	fields := make(map[string]reflect.Value)
	value := reflect.ValueOf(server).Elem()
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if field.Type.Kind() == reflect.Map && field.Type.Key().Kind() == reflect.String {
			fields[field.Name] = value.Field(i)
		}
	}
	return fields
}

func TestHandleMessageDidCloseEvictsEveryPerDocumentMap(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/close-evict.vibe"
	openDoc(t, server, uri, "def helper(n)\n  n\nend\n\ndef run()\n  helper(1)\nend\n")
	// didOpen fills docs, lines, compiled, programs, and published; the
	// completion index and outline are built lazily, so request both to
	// make eviction observable for every map.
	completionLabels(t, server, uri, 5, 2)
	documentSymbolsResult(t, server, uri)

	maps := perURIMaps(server)
	if len(maps) != 7 {
		t.Fatalf("lspServer has %d per-URI maps, want 7; wire new per-document state into evictDocument and this test", len(maps))
	}
	key := reflect.ValueOf(uri)
	for name, field := range maps {
		if !field.MapIndex(key).IsValid() {
			t.Fatalf("per-URI map %q not populated before didClose; adjust the test setup", name)
		}
	}

	closeDoc(t, server, uri)
	for name, field := range maps {
		if entries := field.Len(); entries != 0 {
			t.Errorf("per-URI map %q still has %d entries after didClose", name, entries)
		}
	}
}

func TestHandleMessageDidClosePublishesEmptyDiagnostics(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/close-clear.vibe"
	if diags := openDocDiagnostics(t, server, uri, "def run(\n  1\nend\n"); len(diags) == 0 {
		t.Fatal("expected diagnostics for the broken source")
	}

	messages := closeDoc(t, server, uri)
	if len(messages) != 1 {
		t.Fatalf("didClose responses = %d, want one publishDiagnostics notification", len(messages))
	}
	if messages[0].Method != "textDocument/publishDiagnostics" {
		t.Fatalf("didClose notification method = %q, want textDocument/publishDiagnostics", messages[0].Method)
	}
	params, ok := messages[0].Params.(lspPublishDiagnosticsParams)
	if !ok {
		t.Fatalf("didClose params = %#v, want lspPublishDiagnosticsParams", messages[0].Params)
	}
	if params.URI != uri {
		t.Fatalf("didClose diagnostics uri = %q, want %q", params.URI, uri)
	}
	if len(params.Diagnostics) != 0 {
		t.Fatalf("didClose diagnostics = %#v, want an empty set clearing stale squiggles", params.Diagnostics)
	}
	payload, err := json.Marshal(messages[0])
	if err != nil {
		t.Fatalf("marshal didClose notification: %v", err)
	}
	if !strings.Contains(string(payload), `"diagnostics":[]`) {
		t.Fatalf("didClose notification %s, want an empty diagnostics array, not null", payload)
	}
}

func TestDidCloseThenReopenIdenticalTextRecomputesDiagnostics(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/reopen-same.vibe"
	source := "def run(\n  1\nend\n"

	first := openDocDiagnostics(t, server, uri, source)
	if len(first) == 0 {
		t.Fatal("expected diagnostics for the broken source")
	}
	closeDoc(t, server, uri)

	// The published cache keyed on source text was evicted, so the
	// reopen recompiles from scratch; the result must match what the
	// first open produced.
	reopened := openDocDiagnostics(t, server, uri, source)
	if len(reopened) != len(first) {
		t.Fatalf("reopened diagnostics = %d, want %d as before the close", len(reopened), len(first))
	}
	for i := range first {
		if reopened[i] != first[i] {
			t.Fatalf("reopened diagnostic %d = %#v, want %#v", i, reopened[i], first[i])
		}
	}
}

func TestDidCloseThenReopenDifferentTextDropsStaleState(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/reopen-different.vibe"
	openDoc(t, server, uri, "def alpha()\n  1\nend\n")
	if names := symbolNames(documentSymbolsResult(t, server, uri)); !slices.Equal(names, []string{"alpha"}) {
		t.Fatalf("initial outline = %v, want [alpha]", names)
	}

	closeDoc(t, server, uri)

	// Reopening with different, broken text must produce fresh
	// diagnostics and an outline with no trace of the closed buffer.
	if diags := openDocDiagnostics(t, server, uri, "def beta(\n  2\nend\n"); len(diags) == 0 {
		t.Fatal("expected diagnostics for the broken reopened text")
	}
	if names := symbolNames(documentSymbolsResult(t, server, uri)); slices.Contains(names, "alpha") {
		t.Fatalf("outline after reopen = %v, must not resurrect symbols from the closed buffer", names)
	}

	closeDoc(t, server, uri)

	if diags := openDocDiagnostics(t, server, uri, "def beta()\n  2\nend\n"); len(diags) != 0 {
		t.Fatalf("clean reopened text diagnostics = %#v, want none", diags)
	}
	if names := symbolNames(documentSymbolsResult(t, server, uri)); !slices.Equal(names, []string{"beta"}) {
		t.Fatalf("outline after clean reopen = %v, want [beta]", names)
	}
}

func TestHandleMessageDidCloseUnknownURIIsNoOp(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	kept := "file:///tmp/kept.vibe"
	openDoc(t, server, kept, "def run()\n  1\nend\n")

	messages := closeDoc(t, server, "file:///tmp/never-opened.vibe")
	if len(messages) != 0 {
		t.Fatalf("didClose for unknown uri produced %d messages, want none", len(messages))
	}
	if _, ok := server.docs[kept]; !ok {
		t.Fatal("didClose for unknown uri evicted an unrelated document")
	}
}

func TestDidCloseToleratesSparseServerState(t *testing.T) {
	t.Parallel()
	// Mirror the fuzz harness construction: only docs is allocated, so
	// eviction must tolerate every other per-URI map being nil.
	uri := "file:///tmp/sparse.vibe"
	server := &lspServer{
		engine: vibes.MustNewEngine(vibes.Config{}),
		docs:   map[string]string{uri: "def run()\n  1\nend\n"},
	}

	messages := closeDoc(t, server, uri)
	if len(messages) != 1 {
		t.Fatalf("didClose responses = %d, want the clearing notification", len(messages))
	}
	if len(server.docs) != 0 {
		t.Fatal("didClose did not evict the document text")
	}
}

func TestHandleMessageHoverServesMemberDocs(t *testing.T) {
	t.Parallel()
	// "  name.upcase" puts the member word at characters 7-12.
	value := hoverValue(t, "def run(name)\n  name.upcase\nend\n", 1, 9)
	if !strings.Contains(value, "`upcase(mode = nil) -> string`") {
		t.Fatalf("expected the upcase signature in hover value, got %q", value)
	}
	if !strings.Contains(value, "Unicode") {
		t.Fatalf("expected the upcase description, got %q", value)
	}
	if strings.Contains(value, "---") {
		t.Fatalf("single-receiver member hover must not render merged sections, got %q", value)
	}
}

func TestHandleMessageHoverMergesAmbiguousMemberDocs(t *testing.T) {
	t.Parallel()
	// "  items.size" puts the member word at characters 8-11.
	value := hoverValue(t, "def run(items)\n  items.size\nend\n", 1, 9)
	if !strings.Contains(value, "---") {
		t.Fatalf("expected merged hover sections for the ambiguous member, got %q", value)
	}
	previous := -1
	for _, header := range []string{"`array.size`", "`hash.size`", "`range.size`", "`string.size`"} {
		idx := strings.Index(value, header)
		if idx < 0 {
			t.Fatalf("merged hover missing section %s, got %q", header, value)
		}
		if idx < previous {
			t.Fatalf("merged hover sections out of stable receiver order: %s at %d after %d in %q", header, idx, previous, value)
		}
		previous = idx
	}
}

func TestHandleMessageHoverServesUniversalMemberDocs(t *testing.T) {
	t.Parallel()
	// "  value.itself" puts the member word at characters 8-13.
	value := hoverValue(t, "def run(value)\n  value.itself\nend\n", 1, 10)
	if !strings.Contains(value, "returns the receiver unchanged") {
		t.Fatalf("expected the universal itself doc, got %q", value)
	}
	if strings.Contains(value, "---") {
		t.Fatalf("universal member hover must not merge per-type sections, got %q", value)
	}
}

func TestHandleMessageHoverUnknownMemberFallsBackToClassifier(t *testing.T) {
	t.Parallel()
	value := hoverValue(t, "def run(x)\n  x.frobnify\nend\n", 1, 6)
	if value != "`frobnify`\n\nVibescript symbol" {
		t.Fatalf("unknown member should fall back to the classifier line, got %q", value)
	}
}

func TestHandleMessageHoverMemberDocsRequireDotContext(t *testing.T) {
	t.Parallel()
	// A bare word that happens to be a member name is not a member
	// access, so it must not serve member docs.
	value := hoverValue(t, "def run()\n  upcase\nend\n", 1, 4)
	if value != "`upcase`\n\nVibescript symbol" {
		t.Fatalf("bare member-named word should fall back to the classifier, got %q", value)
	}
}

func TestIsValueMemberAccess(t *testing.T) {
	t.Parallel()
	catalog := newTestBuiltinCatalog(t)
	tests := []struct {
		name      string
		line      string
		character int
		want      bool
	}{
		{name: "value receiver", line: "items.map", character: 7, want: true},
		{name: "numeric receiver", line: "5.minutes", character: 4, want: true},
		{name: "chained leading dot", line: "  .map { |x| x }", character: 4, want: true},
		{name: "bare word", line: "map(x)", character: 1, want: false},
		{name: "namespace receiver", line: "JSON.parse(raw)", character: 7, want: false},
		{name: "scope accessor", line: "Math::PI", character: 7, want: false},
		{name: "range end", line: "1..last", character: 4, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isValueMemberAccess(catalog, []string{tt.line}, 0, tt.character); got != tt.want {
				t.Fatalf("isValueMemberAccess(%q, %d) = %v, want %v", tt.line, tt.character, got, tt.want)
			}
		})
	}
}

const userHoverFixture = `# vibe: strict
# Adds a and b.
# Returns their sum.
def add(a: int, b: int = 2) -> int
  a + b
end

def plain(value)
  value
end

def run()
  add(1)
  plain(2)
end
`

func TestHandleMessageHoverServesUserFunctionDocs(t *testing.T) {
	t.Parallel()
	// "  add(1)" puts the callee at characters 2-4.
	value := hoverValue(t, userHoverFixture, 12, 3)
	if !strings.Contains(value, "```vibe\ndef add(a: int, b: int = …) -> int\n```") {
		t.Fatalf("expected the reconstructed typed signature, got %q", value)
	}
	if !strings.Contains(value, "Adds a and b.\nReturns their sum.") {
		t.Fatalf("expected the doc comment prose, got %q", value)
	}
	if strings.Contains(value, "vibe: strict") {
		t.Fatalf("directive comment lines must be excluded from hover prose, got %q", value)
	}
}

func TestHandleMessageHoverUserFunctionWithoutComment(t *testing.T) {
	t.Parallel()
	value := hoverValue(t, userHoverFixture, 13, 3)
	want := "```vibe\ndef plain(value)\n```"
	if value != want {
		t.Fatalf("uncommented def hover = %q, want signature only %q", value, want)
	}
}

func TestHandleMessageHoverPrefersBuiltinOverUserDef(t *testing.T) {
	t.Parallel()
	source := "def puts(value)\n  value\nend\n\ndef run()\n  puts(1)\nend\n"
	value := hoverValue(t, source, 5, 3)
	if !strings.Contains(value, "Writes each value") {
		t.Fatalf("builtin doc must win over a same-named user def, got %q", value)
	}
	if strings.Contains(value, "```vibe") {
		t.Fatalf("shadowed user def must not contribute its signature, got %q", value)
	}
}

func TestHandleMessageHoverServesUserClassAndEnumDocs(t *testing.T) {
	t.Parallel()
	server := newCompletionTestServer()
	uri := "file:///tmp/user-hover.vibe"
	openDoc(t, server, uri, navigationFixture)

	tests := []struct {
		name      string
		line      int
		character int
		want      string
	}{
		{name: "class", line: 4, character: 8, want: "```vibe\nclass Wallet\n```"},
		{name: "method", line: 5, character: 7, want: "```vibe\ndef balance\n```"},
		{name: "class method", line: 9, character: 12, want: "```vibe\ndef self.empty\n```"},
		{name: "enum", line: 14, character: 6, want: "```vibe\nenum Status\n```"},
		{name: "enum member", line: 16, character: 4, want: "```vibe\nStatus::Published\n```"},
	}
	for _, tt := range tests {
		if got := hoverValueAt(t, server, uri, tt.line, tt.character); got != tt.want {
			t.Fatalf("%s hover = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestHandleMessageHoverUserSetterDef(t *testing.T) {
	t.Parallel()
	source := `class Counter
  # Stores the count.
  def value=(n)
    @value = n
  end
end

def run()
  c = Counter.new
  c.value = 3
end
`
	// "  c.value = 3" puts the setter word at characters 4-8.
	value := hoverValue(t, source, 9, 5)
	if !strings.Contains(value, "```vibe\ndef value=(n)\n```") {
		t.Fatalf("expected the setter signature, got %q", value)
	}
	if !strings.Contains(value, "Stores the count.") {
		t.Fatalf("expected the setter doc comment, got %q", value)
	}
}

func TestMemberCompletionItemsCarryUnambiguousDocs(t *testing.T) {
	t.Parallel()
	items := memberCompletionItems()

	upcase := findCompletionItem(t, items, "upcase")
	doc, ok := upcase["documentation"].(map[string]any)
	if !ok {
		t.Fatalf("upcase documentation = %#v, want markdown contents", upcase["documentation"])
	}
	if doc["kind"] != "markdown" {
		t.Fatalf("upcase documentation kind = %#v, want markdown", doc["kind"])
	}
	if value, _ := doc["value"].(string); !strings.Contains(value, "Unicode") {
		t.Fatalf("upcase documentation value = %#v, want the stdlib description", doc["value"])
	}

	itself := findCompletionItem(t, items, "itself")
	if _, ok := itself["documentation"].(map[string]any); !ok {
		t.Fatalf("itself documentation = %#v, want the universal helper doc", itself["documentation"])
	}

	// An ambiguous name has no receiver context in the type-unaware
	// member list, so it must not pick one type's documentation.
	size := findCompletionItem(t, items, "size")
	if _, ok := size["documentation"]; ok {
		t.Fatalf("size documentation = %#v, want none for an ambiguous member", size["documentation"])
	}
}

// TestMemberCompletionItemsCarryContractSignatures verifies that members
// registered in the runtime contract registry surface receiver-qualified
// signatures in completion documentation, including for ambiguous names.
func TestMemberCompletionItemsCarryContractSignatures(t *testing.T) {
	t.Parallel()
	items := memberCompletionItems()

	cases := []struct {
		label      string
		signatures []string
	}{
		{"at", []string{"`array.at(index)`"}},
		{"fetch", []string{"`array.fetch(index, default?) { ... }`"}},
		{"slice", []string{"`array.slice(start, length?)`", "`string.slice(start, length?)`"}},
		{"to_i", []string{"`string.to_i() -> int`", "`duration.to_i -> int`"}},
		{"nil?", []string{"`nil?() -> bool`"}},
		{"eql?", []string{"`duration.eql?(other) -> bool`", "`time.eql?(other) -> bool`", "`eql?(other) -> bool`"}},
	}
	for _, tc := range cases {
		item := findCompletionItem(t, items, tc.label)
		doc, ok := item["documentation"].(map[string]any)
		if !ok {
			t.Fatalf("%s documentation = %#v, want markdown contents", tc.label, item["documentation"])
		}
		value, _ := doc["value"].(string)
		for _, signature := range tc.signatures {
			if !strings.Contains(value, signature) {
				t.Errorf("%s documentation = %q, want it to contain %q", tc.label, value, signature)
			}
		}
	}
}

// TestMemberDocsMatchRuntimeMembers is the value-member documentation
// drift gate: every parsed doc entry must name a member the runtime
// actually dispatches for that receiver type, so the table can never
// hold phantom documentation. Coverage in the other direction (runtime
// members without docs) is reported informationally; the known-missing
// set is tracked in the PR rather than gated, since closing it requires
// writing new reference documentation.
func TestMemberDocsMatchRuntimeMembers(t *testing.T) {
	t.Parallel()
	docs := memberDocs()
	runtime := vibes.MemberCompletionNames()

	// Canaries: one member per parsed source section, so a silently
	// dropped section (a renamed heading, a changed bullet shape)
	// fails loudly instead of shrinking the table.
	canaries := []struct{ receiver, name string }{
		{"string", "upcase"},
		{"array", "map"},
		{"array", "take"},
		{"hash", "fetch"},
		{"hash", "transform_keys"},
		{"int", "times"},
		{"float", "nan?"},
		{"money", "cents"},
		{"duration", "ago"},
		{"time", "strftime"},
		{"symbol", "id2name"},
		{"range", "cover?"},
		{"regex", "source"},
	}
	for _, canary := range canaries {
		if !slices.ContainsFunc(docs.entries[canary.name], func(entry memberDocEntry) bool {
			return entry.Receiver == canary.receiver
		}) {
			t.Errorf("member docs lost the %s.%s entry; check the section mappings in lspdocs.go", canary.receiver, canary.name)
		}
	}
	for _, name := range []string{"itself", "tap", "eql?", "respond_to?"} {
		if _, ok := docs.universal[name]; !ok {
			t.Errorf("member docs lost the universal %s entry", name)
		}
	}
	// A universal entry must be dispatched by every runtime receiver;
	// partial conversions like to_s (rejected on arrays, hashes, and
	// ranges) demote to typed entries instead.
	for name := range docs.universal {
		for receiver := range memberDocReceivers {
			if !runtimeMemberIndex[name][receiver] {
				t.Errorf("universal entry %s is not dispatched on %s; it must demote to typed entries", name, receiver)
			}
		}
	}
	for _, name := range []string{"to_s", "string"} {
		if _, ok := docs.universal[name]; ok {
			t.Errorf("%s registered as universal but the runtime rejects it on some receivers", name)
		}
		md := memberDocMarkdown(name)
		if md == "" {
			t.Errorf("memberDocMarkdown(%q) = empty, want the demoted typed documentation", name)
			continue
		}
		sections := strings.Split(md, "\n\n---\n\n")
		seen := make(map[string]bool)
		for _, section := range sections {
			body := section
			if _, after, ok := strings.Cut(section, "\n\n"); ok {
				body = after
			}
			if seen[body] {
				t.Errorf("memberDocMarkdown(%q) repeats identical section bodies:\n%s", name, md)
			}
			seen[body] = true
		}
	}
	// Bang variants resolve through the base-entry fallback rather than
	// table entries; the composed hover must carry the in-place note and
	// the base documentation.
	for _, name := range []string{"strip!", "lstrip!"} {
		md := memberDocMarkdown(name)
		if !strings.Contains(md, "In-place variant of") {
			t.Errorf("memberDocMarkdown(%q) = %q, want the in-place bang note", name, md)
		}
	}
	// sort! used to stand here as the one bang with a table entry of its own,
	// which won over the fallback. The array bang forms are gone (ADR-006 item
	// 2) and every remaining bang is a string form documented as a group, so
	// there is no longer a bang whose table entry could shadow the fallback.
	if md := memberDocMarkdown("sub!"); !strings.Contains(md, "never matched") {
		t.Errorf("memberDocMarkdown(sub!) = %q, want the match-keyed note", md)
	}

	// Hard gate: no phantom docs.
	union := make(map[string]struct{})
	for receiver, members := range runtime {
		if _, ok := memberDocReceivers[receiver]; !ok {
			t.Errorf("runtime receiver %q missing from memberDocReceivers", receiver)
		}
		for _, member := range members {
			union[member] = struct{}{}
		}
	}
	for name, entries := range docs.entries {
		for _, entry := range entries {
			members, known := runtime[entry.Receiver]
			if !known {
				t.Errorf("documented member %s.%s names a receiver type the runtime does not dispatch", entry.Receiver, name)
				continue
			}
			if !slices.Contains(members, name) {
				t.Errorf("documented member %s.%s does not exist in the runtime", entry.Receiver, name)
			}
		}
	}
	for name := range docs.universal {
		if _, ok := union[name]; !ok {
			t.Errorf("documented universal member %s does not exist on any runtime receiver", name)
		}
	}

	// Coverage report: what fraction of runtime members carry docs.
	receivers := make([]string, 0, len(runtime))
	for receiver := range runtime {
		receivers = append(receivers, receiver)
	}
	slices.Sort(receivers)
	total, documented := 0, 0
	for _, receiver := range receivers {
		members := runtime[receiver]
		covered := 0
		var missing []string
		for _, member := range members {
			_, universal := docs.universal[member]
			if universal || slices.ContainsFunc(docs.entries[member], func(entry memberDocEntry) bool {
				return entry.Receiver == receiver
			}) {
				covered++
				continue
			}
			missing = append(missing, member)
		}
		slices.Sort(missing)
		total += len(members)
		documented += covered
		t.Logf("%s: %d/%d members documented; missing %v", receiver, covered, len(members), missing)
	}
	t.Logf("overall member doc coverage: %d/%d (%.1f%%)", documented, total, 100*float64(documented)/float64(total))
	if documented*4 < total*3 {
		t.Errorf("member doc coverage %d/%d fell below 75%%; the parser likely lost a source", documented, total)
	}
}

func TestHoverUserSymbolScopePreference(t *testing.T) {
	t.Parallel()
	source := "class Alpha\n" + // line 0
		"  # Runs the alpha path.\n" +
		"  def run(a: int) -> int\n" + // line 2
		"    a\n" +
		"  end\n" +
		"end\n" +
		"\n" +
		"class Beta\n" + // line 7
		"  # Runs the beta path.\n" +
		"  def run(b: string) -> string\n" + // line 9
		"    helper\n" + // line 10
		"  end\n" +
		"\n" +
		"  def helper\n" +
		"    run(\"x\")\n" + // line 14: call inside Beta
		"  end\n" +
		"end\n" +
		"\n" +
		"enum First\n" + // line 18
		"  Draft\n" + // line 19
		"end\n" +
		"\n" +
		"enum Second\n" + // line 22
		"  Draft\n" + // line 23
		"end\n" +
		"\n" +
		"# Runs the top-level path.\n" +
		"def run\n" + // line 27
		"end\n" +
		"run\n" // line 29: top-level gap call

	tests := []struct {
		name      string
		line      int
		character int
		want      string
	}{
		{name: "alpha declaration", line: 2, character: 7, want: "Runs the alpha path."},
		{name: "beta declaration", line: 9, character: 7, want: "Runs the beta path."},
		{name: "call inside beta scope", line: 14, character: 5, want: "Runs the beta path."},
		{name: "alpha signature type", line: 2, character: 7, want: "def run(a: int) -> int"},
		{name: "beta signature type", line: 9, character: 7, want: "def run(b: string) -> string"},
		{name: "first enum member", line: 19, character: 3, want: "First::Draft"},
		{name: "second enum member", line: 23, character: 3, want: "Second::Draft"},
		{name: "gap after classes prefers top-level def", line: 29, character: 1, want: "Runs the top-level path."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hoverValue(t, source, tt.line, tt.character)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("hover(%d:%d) = %q, want it to contain %q", tt.line, tt.character, got, tt.want)
			}
		})
	}
}

func TestHoverSetterPreferenceAtWriteSites(t *testing.T) {
	t.Parallel()
	source := "class Counter\n" +
		"  # Reads the current value.\n" +
		"  def value\n" +
		"    @value\n" +
		"  end\n" +
		"\n" +
		"  # Writes the current value.\n" +
		"  def value=(next_value)\n" +
		"    @value = next_value\n" +
		"  end\n" +
		"end\n" +
		"\n" +
		"c = Counter.new\n" +
		"c.value = 3\n" + // line 13: write site
		"x = c.value\n" + // line 14: read site
		"ok = c.value == 3\n" // line 15: comparison, not a write

	tests := []struct {
		name      string
		line      int
		character int
		want      string
	}{
		{name: "write site prefers setter", line: 13, character: 3, want: "Writes the current value."},
		{name: "read site keeps getter", line: 14, character: 7, want: "Reads the current value."},
		{name: "comparison keeps getter", line: 15, character: 8, want: "Reads the current value."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hoverValue(t, source, tt.line, tt.character)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("hover(%d:%d) = %q, want it to contain %q", tt.line, tt.character, got, tt.want)
			}
		})
	}
}

func TestHoverDottedMemberBeatsGlobalBuiltin(t *testing.T) {
	t.Parallel()
	got := hoverValue(t, "price = money(\"$3.50\")\nlabel = price.format\n", 1, 15)
	if strings.Contains(got, "format(pattern, *values)") {
		t.Fatalf("hover(price.format) served the global format builtin: %q", got)
	}
	if !strings.Contains(got, "format") || strings.Contains(got, "Vibescript symbol") {
		t.Fatalf("hover(price.format) = %q, want member documentation", got)
	}
}

func TestHoverQualifiedAndNestedUserSymbols(t *testing.T) {
	t.Parallel()
	source := "enum First\n" + // 0
		"  Draft\n" + // 1
		"end\n" +
		"\n" +
		"enum Second\n" + // 4
		"  Draft\n" + // 5
		"end\n" +
		"\n" +
		"module Outer\n" + // 8
		"  module Inner\n" + // 9
		"    # Inner helper.\n" + // 10
		"    def self.helper\n" + // 11
		"      1\n" +
		"    end\n" +
		"  end\n" + // 14
		"\n" +
		"  # Outer helper.\n" + // 16
		"  def self.helper\n" + // 17
		"    2\n" +
		"  end\n" +
		"\n" +
		"  def self.run\n" + // 21
		"    helper\n" + // 22: call in Outer, after Inner
		"  end\n" +
		"end\n" +
		"\n" +
		"a = First::Draft\n" + // 26
		"b = Second::Draft\n" // 27

	tests := []struct {
		name      string
		line      int
		character int
		want      string
	}{
		{name: "qualified first enum usage", line: 26, character: 12, want: "First::Draft"},
		{name: "qualified second enum usage", line: 27, character: 13, want: "Second::Draft"},
		{name: "outer helper wins after nested module", line: 22, character: 5, want: "Outer helper."},
		{name: "inner helper at its declaration", line: 11, character: 14, want: "Inner helper."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hoverValue(t, source, tt.line, tt.character)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("hover(%d:%d) = %q, want it to contain %q", tt.line, tt.character, got, tt.want)
			}
		})
	}
}

func TestHoverTypedRequiredKeywordSignature(t *testing.T) {
	t.Parallel()
	source := "# Greets loudly.\ndef f(name: string:)\n  name\nend\n"
	got := hoverValue(t, source, 1, 5)
	if !strings.Contains(got, "def f(name: string:)") {
		t.Fatalf("hover = %q, want the declaration syntax def f(name: string:)", got)
	}
	if strings.Contains(got, "name:: string") {
		t.Fatalf("hover = %q, renders the invalid name:: string form", got)
	}
}

func TestHoverQualifiedAccessExcludesTopLevel(t *testing.T) {
	t.Parallel()
	source := "# Saves everything at once.\n" +
		"def save\n" + // line 1
		"end\n" +
		"\n" +
		"class Client\n" +
		"  # Saves this client.\n" +
		"  def save\n" + // line 6
		"  end\n" +
		"end\n" +
		"\n" +
		"client.save\n" + // line 10: qualified, unmatched receiver
		"save\n" // line 11: bare call
	if got := hoverValue(t, source, 10, 8); !strings.Contains(got, "Saves this client.") {
		t.Fatalf("hover(client.save) = %q, want the scoped method docs", got)
	}
	if got := hoverValue(t, source, 11, 1); !strings.Contains(got, "Saves everything at once.") {
		t.Fatalf("hover(save) = %q, want the top-level docs", got)
	}
}

func TestHoverInstanceReceiverSkipsClassMethods(t *testing.T) {
	t.Parallel()
	source := "class Client\n" +
		"  # Saves this client instance.\n" +
		"  def save\n" + // line 2
		"  end\n" +
		"\n" +
		"  # Saves every client at once.\n" +
		"  def self.save\n" + // line 6
		"  end\n" +
		"end\n" +
		"\n" +
		"client.save\n" + // line 10: instance receiver
		"Client.save\n" // line 11: class receiver
	if got := hoverValue(t, source, 10, 8); !strings.Contains(got, "Saves this client instance.") {
		t.Fatalf("hover(client.save) = %q, want the instance method docs", got)
	}
	if got := hoverValue(t, source, 11, 8); !strings.Contains(got, "Saves every client at once.") {
		t.Fatalf("hover(Client.save) = %q, want the class method docs", got)
	}
}

func TestCompletionNamespaceDocumentation(t *testing.T) {
	t.Parallel()
	for _, item := range testCompletionItems(t) {
		if item["label"] != "JSON" {
			continue
		}
		if item["detail"] != "namespace" {
			t.Fatalf("JSON completion detail = %v, want namespace", item["detail"])
		}
		doc, ok := item["documentation"].(map[string]any)
		if !ok || !strings.Contains(doc["value"].(string), "Members: `parse`") {
			t.Fatalf("JSON completion documentation = %v, want the namespace markdown with members", item["documentation"])
		}
		return
	}
	t.Fatal("JSON completion item not found")
}

func TestHoverQualifiedNestedModuleReference(t *testing.T) {
	t.Parallel()
	source := "module Outer\n" +
		"  # Inner workings.\n" +
		"  module Inner\n" + // line 2
		"  end\n" +
		"end\n" +
		"\n" +
		"x = Outer::Inner\n" // line 6
	got := hoverValue(t, source, 6, 12)
	if !strings.Contains(got, "module Inner") || !strings.Contains(got, "Inner workings.") {
		t.Fatalf("hover(Outer::Inner) = %q, want the nested module signature and comment", got)
	}
}

func TestHoverIncludeExtendDirectiveIsContextual(t *testing.T) {
	t.Parallel()
	assigned := "include = 1\ninclude\n"
	if got := hoverValue(t, assigned, 0, 0); strings.Contains(got, "Removed mixin") {
		t.Fatalf("hover(include =) = %q, must not serve mixin-removal docs", got)
	}
	if got := hoverValue(t, assigned, 1, 0); strings.Contains(got, "Removed mixin") {
		t.Fatalf("hover(include read) = %q, must not serve mixin-removal docs", got)
	}
	called := "def include(x)\n  x\nend\ninclude Naming\n"
	if got := hoverValue(t, called, 3, 0); strings.Contains(got, "Removed mixin") {
		t.Fatalf("hover(top-level include Naming) = %q, must not serve mixin-removal docs", got)
	}
	directive := "class C\n  include Naming\nend\n"
	if got := hoverValue(t, directive, 1, 2); !strings.Contains(got, "Removed mixin") {
		t.Fatalf("hover(include Naming) = %q, want mixin-removal docs", got)
	}
	paren := "class C\n  include(Naming)\nend\n"
	if got := hoverValue(t, paren, 1, 2); !strings.Contains(got, "Removed mixin") {
		t.Fatalf("hover(include(Naming)) = %q, want mixin-removal docs", got)
	}
	bare := "class C\n  include\nend\n"
	if got := hoverValue(t, bare, 1, 2); !strings.Contains(got, "Removed mixin") {
		t.Fatalf("hover(bare include in class) = %q, want mixin-removal docs", got)
	}
}

func TestHoverBareAssignmentIsNotASetterCall(t *testing.T) {
	t.Parallel()
	source := "class Counter\n" +
		"  # Reads the current value.\n" +
		"  def value\n" +
		"  end\n" +
		"\n" +
		"  # Writes the current value.\n" +
		"  def value=(v)\n" +
		"  end\n" +
		"end\n" +
		"\n" +
		"value = 3\n" + // line 10: bare local write
		"c = Counter.new\n" +
		"self.value = 4\n" // line 12: member write
	if got := hoverValue(t, source, 10, 2); strings.Contains(got, "Writes the current value.") {
		t.Fatalf("hover(bare value =) = %q, must not serve setter docs", got)
	}
	if got := hoverValue(t, source, 12, 7); !strings.Contains(got, "Writes the current value.") {
		t.Fatalf("hover(self.value =) = %q, want setter docs", got)
	}
}

func TestHoverClassReceiverWithoutClassMethod(t *testing.T) {
	t.Parallel()
	source := "class Client\n" +
		"  # Saves this client instance.\n" +
		"  def save\n" +
		"  end\n" +
		"end\n" +
		"\n" +
		"Client.save\n" + // line 6: invalid class dispatch
		"client.save\n" // line 7: instance dispatch
	if got := hoverValue(t, source, 6, 8); strings.Contains(got, "Saves this client instance.") {
		t.Fatalf("hover(Client.save) = %q, must not serve instance method docs", got)
	}
	if got := hoverValue(t, source, 7, 8); !strings.Contains(got, "Saves this client instance.") {
		t.Fatalf("hover(client.save) = %q, want the instance method docs", got)
	}
}

func TestHoverSetterDeclarationName(t *testing.T) {
	t.Parallel()
	source := "class Counter\n" +
		"  # Reads the current value.\n" +
		"  def value\n" +
		"  end\n" +
		"\n" +
		"  # Writes the current value.\n" +
		"  def value=(v)\n" + // line 6
		"  end\n" +
		"end\n" +
		"\n" +
		"value=3\n" // line 10: flush bare local write
	if got := hoverValue(t, source, 6, 8); !strings.Contains(got, "Writes the current value.") {
		t.Fatalf("hover(def value=) = %q, want the setter docs", got)
	}
	if got := hoverValue(t, source, 10, 2); strings.Contains(got, "Writes the current value.") {
		t.Fatalf("hover(flush bare write) = %q, must not serve setter docs", got)
	}
}

func TestHoverScopedOnlyNamesStayOutOfTopLevel(t *testing.T) {
	t.Parallel()
	source := "module Outer\n" +
		"  module Inner\n" +
		"    # Inner helper.\n" +
		"    def helper\n" +
		"    end\n" +
		"  end\n" + // line 5
		"  helper\n" + // line 6: outer body call after Inner's end
		"end\n" +
		"\n" +
		"class A\n" +
		"  # Runs A.\n" +
		"  def run\n" +
		"  end\n" +
		"end\n" +
		"\n" +
		"run\n" // line 15: bare top-level, no top-level def
	if got := hoverValue(t, source, 15, 1); strings.Contains(got, "Runs A.") {
		t.Fatalf("hover(top-level run) = %q, must not serve the scoped method", got)
	}
	if got := hoverValue(t, source, 6, 3); strings.Contains(got, "Inner helper.") {
		t.Fatalf("hover(outer-body helper) = %q, must not serve the nested module's method", got)
	}
}
