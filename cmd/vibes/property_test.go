package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/parser"
	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/value"
	"pgregory.net/rapid"
)

func TestRapidFormatGeneratedValidSourceIsIdempotent(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		source := drawRapidVibeProgram(rt)
		if _, parseErrors := parser.Parse(source); len(parseErrors) > 0 {
			rt.Fatalf("parser.Parse(drawRapidVibeProgram()) errors = %v for source %q, want none", parseErrors, source)
		}

		formatted := formatVibeSource(source)
		if !strings.HasSuffix(formatted, "\n") {
			rt.Fatalf("formatVibeSource(%q) = %q, want trailing newline", source, formatted)
		}
		if strings.Contains(formatted, "\r") {
			rt.Fatalf("formatVibeSource(%q) = %q, want no carriage returns", source, formatted)
		}
		for lineNumber, line := range strings.Split(strings.TrimSuffix(formatted, "\n"), "\n") {
			if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
				rt.Fatalf("formatVibeSource(%q) line %d = %q, want no trailing horizontal whitespace", source, lineNumber+1, line)
			}
		}
		if second := formatVibeSource(formatted); second != formatted {
			rt.Fatalf("formatVibeSource(formatVibeSource(%q)) = %q, want %q", source, second, formatted)
		}
		if _, parseErrors := parser.Parse(formatted); len(parseErrors) > 0 {
			rt.Fatalf("parser.Parse(formatVibeSource(%q)) errors = %v, want none", source, parseErrors)
		}

		engine, err := vibes.NewEngine(vibes.Config{})
		if err != nil {
			rt.Fatalf("vibes.NewEngine(vibes.Config{}) error = %v, want nil", err)
		}
		if _, err := engine.Compile(formatted); err != nil {
			rt.Fatalf("Compile(formatVibeSource(%q)) error = %v, want nil", source, err)
		}
	})
}

func TestRapidFormatGeneratedCompoundSourceCompiles(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		program := drawRapidCompoundProgram(rt)
		if _, parseErrors := parser.Parse(program.source); len(parseErrors) > 0 {
			rt.Fatalf("parser.Parse(drawRapidCompoundProgram()) errors = %v for source %q, want none", parseErrors, program.source)
		}

		formatted := formatVibeSource(program.source)
		if _, parseErrors := parser.Parse(formatted); len(parseErrors) > 0 {
			rt.Fatalf("parser.Parse(formatVibeSource(%q)) errors = %v, want none", program.source, parseErrors)
		}

		engine, err := vibes.NewEngine(vibes.Config{})
		if err != nil {
			rt.Fatalf("vibes.NewEngine(vibes.Config{}) error = %v, want nil", err)
		}
		script, err := engine.Compile(formatted)
		if err != nil {
			rt.Fatalf("Compile(formatVibeSource(%q)) error = %v, want nil", program.source, err)
		}
		result, err := script.Call(rt.Context(), program.function, program.args, vibes.CallOptions{})
		if err != nil {
			rt.Fatalf("Call(%s, args=%v) for source %q error = %v, want nil", program.function, program.args, formatted, err)
		}
		if program.checkResult && !result.Equal(program.want) {
			rt.Fatalf("Call(%s, args=%v) for source %q = %s, want %s", program.function, program.args, formatted, result.String(), program.want.String())
		}
	})
}

func TestRapidComputeModulePathsMatchesDirectoryModel(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()
		scriptDir := filepath.Join(root, "scripts")
		extraA := filepath.Join(root, "modules-a")
		extraB := filepath.Join(root, "modules-b")
		for _, dir := range []string{scriptDir, extraA, extraB} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				rt.Fatalf("os.MkdirAll(%q) error = %v, want nil", dir, err)
			}
		}

		scriptPath := filepath.Join(scriptDir, "main.vibe")
		choices := []string{scriptDir, extraA, extraB}
		indexes := rapid.SliceOfN(rapid.IntRange(0, len(choices)-1), 0, 12).Draw(rt, "module path indexes")
		extras := make([]string, len(indexes))
		for i, index := range indexes {
			extras[i] = choices[index]
		}

		var modulePaths pathList
		for _, extra := range extras {
			if err := modulePaths.Set(extra); err != nil {
				rt.Fatalf("pathList.Set(%q) error = %v, want nil", extra, err)
			}
		}
		if got, want := modulePaths.String(), strings.Join(extras, string(os.PathListSeparator)); got != want {
			rt.Fatalf("pathList.String() after Set(%v) = %q, want %q", extras, got, want)
		}

		got, err := computeModulePaths(scriptPath, modulePaths)
		if err != nil {
			rt.Fatalf("computeModulePaths(%q, %v) error = %v, want nil", scriptPath, extras, err)
		}
		want := modelModulePaths(rt, scriptDir, extras)
		if len(got) != len(want) {
			rt.Fatalf("computeModulePaths(%q, %v) length = %d (%v), want %d (%v)", scriptPath, extras, len(got), got, len(want), want)
		}
		for i := range got {
			if got[i] != want[i] {
				rt.Fatalf("computeModulePaths(%q, %v)[%d] = %q, want %q; got all %v want all %v", scriptPath, extras, i, got[i], want[i], got, want)
			}
		}
	})
}

func TestRapidLSPDocumentStateModel(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		server := &lspServer{
			engine: vibes.MustNewEngine(vibes.Config{}),
			docs:   make(map[string]string),
		}
		model := make(map[string]string)
		actions := rapid.SliceOfN(rapidLSPActionGenerator(), 1, 20).Draw(rt, "lsp actions")

		for step, action := range actions {
			incoming := action.message(step)
			messages := server.handleMessage(incoming)
			action.apply(model)
			if !maps.Equal(server.docs, model) {
				rt.Fatalf("lsp step %d %s docs = %v, want %v", step, action.kind, server.docs, model)
			}
			validateRapidLSPMessages(rt, step, action, incoming, messages)
		}
	})
}

func drawRapidVibeProgram(rt *rapid.T) string {
	functionName := fmt.Sprintf("prop_%d", rapid.IntRange(0, 1_000_000).Draw(rt, "function suffix"))
	paramCount := rapid.IntRange(0, 3).Draw(rt, "parameter count")
	params := make([]string, paramCount)
	for i := range params {
		params[i] = fmt.Sprintf("p%d", i)
	}

	lines := []string{
		fmt.Sprintf("def %s(%s)%s", functionName, strings.Join(params, ", "), drawRapidTrailingWhitespace(rt)),
	}

	if len(params) > 0 && rapid.Bool().Draw(rt, "include assignment") {
		lines = append(lines, fmt.Sprintf("  local = %s%s", params[0], drawRapidTrailingWhitespace(rt)))
	}
	lines = append(lines, fmt.Sprintf("  return %s%s", drawRapidVibeExpression(rt, params, 0), drawRapidTrailingWhitespace(rt)))
	lines = append(lines, "end"+drawRapidTrailingWhitespace(rt))
	return strings.Join(lines, "\n") + "\n"
}

func drawRapidVibeExpression(rt *rapid.T, params []string, depth int) string {
	choices := []string{"nil", "bool", "int", "string"}
	if len(params) > 0 {
		choices = append(choices, "param")
	}
	if depth < 2 {
		choices = append(choices, "array", "hash", "binary")
	}

	switch rapid.SampledFrom(choices).Draw(rt, "expression kind") {
	case "nil":
		return "nil"
	case "bool":
		if rapid.Bool().Draw(rt, "bool literal") {
			return "true"
		}
		return "false"
	case "int":
		return strconv.Itoa(rapid.IntRange(-1000, 1000).Draw(rt, "int literal"))
	case "string":
		return strconv.Quote(rapid.StringMatching(`[A-Za-z0-9 _.\-]{0,24}`).Draw(rt, "string literal"))
	case "param":
		return rapid.SampledFrom(params).Draw(rt, "param reference")
	case "array":
		size := rapid.IntRange(1, 4).Draw(rt, "array size")
		items := make([]string, size)
		for i := range items {
			items[i] = drawRapidVibeExpression(rt, params, depth+1)
		}
		return "[" + strings.Join(items, ", ") + "]"
	case "hash":
		size := rapid.IntRange(1, 4).Draw(rt, "hash size")
		pairs := make([]string, size)
		for i := range pairs {
			pairs[i] = fmt.Sprintf("k%d: %s", i, drawRapidVibeExpression(rt, params, depth+1))
		}
		return "{" + strings.Join(pairs, ", ") + "}"
	default:
		left := rapid.IntRange(-1000, 1000).Draw(rt, "left int")
		right := rapid.IntRange(-1000, 1000).Draw(rt, "right int")
		return fmt.Sprintf("(%d + %d)", left, right)
	}
}

func drawRapidTrailingWhitespace(rt *rapid.T) string {
	spaces := strings.Repeat(" ", rapid.IntRange(0, 3).Draw(rt, "trailing spaces"))
	tabs := strings.Repeat("\t", rapid.IntRange(0, 1).Draw(rt, "trailing tabs"))
	return spaces + tabs
}

type rapidCompoundProgram struct {
	source      string
	function    string
	args        []value.Value
	want        value.Value
	checkResult bool
}

func drawRapidCompoundProgram(rt *rapid.T) rapidCompoundProgram {
	suffix := rapid.IntRange(0, 1_000_000).Draw(rt, "compound suffix")
	function := fmt.Sprintf("compound_%d", suffix)

	switch rapid.IntRange(0, 4).Draw(rt, "compound kind") {
	case 0:
		input := int64(rapid.IntRange(-20, 20).Draw(rt, "if input"))
		threshold := int64(rapid.IntRange(-20, 20).Draw(rt, "if threshold"))
		want := threshold
		if input > threshold {
			want = input
		}
		source := fmt.Sprintf(`def %s(n)
  if n > %d
    return n
  else
    return %d
  end
end
`, function, threshold, threshold)
		return rapidCompoundProgram{
			source:      source,
			function:    function,
			args:        []value.Value{value.NewInt(input)},
			want:        value.NewInt(want),
			checkResult: true,
		}
	case 1:
		boxClass := fmt.Sprintf("RapidBox%d", suffix)
		n := int64(rapid.IntRange(-100, 100).Draw(rt, "box value"))
		source := fmt.Sprintf(`class %s
  property value

  def initialize(@value)
  end

  def read()
    return @value
  end
end

def %s()
  box = %s.new(%d)
  return box.read()
end
`, boxClass, function, boxClass, n)
		return rapidCompoundProgram{
			source:      source,
			function:    function,
			want:        value.NewInt(n),
			checkResult: true,
		}
	case 2:
		enumName := fmt.Sprintf("RapidStatus%d", suffix)
		flag := rapid.Bool().Draw(rt, "enum flag")
		source := fmt.Sprintf(`enum %s
  Draft
  Published
end

def %s(flag)
  if flag
    return %s::Draft
  else
    return %s::Published
  end
end
`, enumName, function, enumName, enumName)
		return rapidCompoundProgram{
			source:   source,
			function: function,
			args:     []value.Value{value.NewBool(flag)},
		}
	case 3:
		limit := int64(rapid.IntRange(1, 25).Draw(rt, "loop limit"))
		source := fmt.Sprintf(`def %s(limit)
  total = 0
  for i in 1..limit
    total = total + i
  end
  return total
end
`, function)
		return rapidCompoundProgram{
			source:      source,
			function:    function,
			args:        []value.Value{value.NewInt(limit)},
			want:        value.NewInt(limit * (limit + 1) / 2),
			checkResult: true,
		}
	default:
		left := int64(rapid.IntRange(-100, 100).Draw(rt, "left row value"))
		right := int64(rapid.IntRange(-100, 100).Draw(rt, "right row value"))
		source := fmt.Sprintf(`def %s()
  rows = [{ value: %d }, { value: %d }]
  return rows[0][:value] + rows[1][:value]
end
`, function, left, right)
		return rapidCompoundProgram{
			source:      source,
			function:    function,
			want:        value.NewInt(left + right),
			checkResult: true,
		}
	}
}

func modelModulePaths(rt *rapid.T, scriptDir string, extras []string) []string {
	seen := make(map[string]struct{}, len(extras)+1)
	out := make([]string, 0, len(extras)+1)
	for _, dir := range append([]string{scriptDir}, extras...) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			rt.Fatalf("filepath.Abs(%q) error = %v, want nil", dir, err)
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out
}

type rapidLSPAction struct {
	kind      string
	uri       string
	source    string
	line      int
	character int
}

func rapidLSPActionGenerator() *rapid.Generator[rapidLSPAction] {
	return rapid.Custom(func(rt *rapid.T) rapidLSPAction {
		return rapidLSPAction{
			kind:      rapid.SampledFrom([]string{"initialize", "initialized", "didOpen", "didChange", "completion", "hover", "shutdown", "exit", "unknown"}).Draw(rt, "lsp action kind"),
			uri:       "file:///tmp/" + drawRapidLSPIdentifier(rt, "lsp uri") + ".vibe",
			source:    drawRapidLSPSource(rt),
			line:      rapid.IntRange(-2, 8).Draw(rt, "lsp line"),
			character: rapid.IntRange(-4, 80).Draw(rt, "lsp character"),
		}
	})
}

func (a rapidLSPAction) message(step int) lspInboundMessage {
	id := json.RawMessage(strconv.Itoa(step + 1))
	switch a.kind {
	case "initialize":
		return lspInboundMessage{JSONRPC: "2.0", ID: &id, Method: "initialize"}
	case "initialized":
		return lspInboundMessage{JSONRPC: "2.0", Method: "initialized"}
	case "didOpen":
		return lspInboundMessage{
			JSONRPC: "2.0",
			Method:  "textDocument/didOpen",
			Params: mustMarshalRapidJSON(map[string]any{
				"textDocument": map[string]any{"uri": a.uri, "text": a.source},
			}),
		}
	case "didChange":
		return lspInboundMessage{
			JSONRPC: "2.0",
			Method:  "textDocument/didChange",
			Params: mustMarshalRapidJSON(map[string]any{
				"textDocument":   map[string]any{"uri": a.uri},
				"contentChanges": []map[string]any{{"text": a.source + "\nignored"}, {"text": a.source}},
			}),
		}
	case "completion":
		return lspInboundMessage{JSONRPC: "2.0", ID: &id, Method: "textDocument/completion"}
	case "hover":
		return lspInboundMessage{
			JSONRPC: "2.0",
			ID:      &id,
			Method:  "textDocument/hover",
			Params: mustMarshalRapidJSON(map[string]any{
				"textDocument": map[string]any{"uri": a.uri},
				"position":     map[string]any{"line": a.line, "character": a.character},
			}),
		}
	case "shutdown":
		return lspInboundMessage{JSONRPC: "2.0", ID: &id, Method: "shutdown"}
	case "exit":
		return lspInboundMessage{JSONRPC: "2.0", Method: "exit"}
	default:
		return lspInboundMessage{JSONRPC: "2.0", ID: &id, Method: "rapid/unknown"}
	}
}

func (a rapidLSPAction) apply(model map[string]string) {
	switch a.kind {
	case "didOpen", "didChange":
		model[a.uri] = a.source
	}
}

func validateRapidLSPMessages(rt *rapid.T, step int, action rapidLSPAction, incoming lspInboundMessage, messages []lspOutboundMessage) {
	for i, message := range messages {
		if message.JSONRPC != "2.0" {
			rt.Fatalf("lsp step %d %s message[%d].JSONRPC = %q, want 2.0", step, action.kind, i, message.JSONRPC)
		}
		if _, err := json.Marshal(message); err != nil {
			rt.Fatalf("lsp step %d %s json.Marshal(message[%d]) error = %v, want nil", step, action.kind, i, err)
		}
	}

	switch action.kind {
	case "didOpen", "didChange":
		if len(messages) != 1 {
			rt.Fatalf("lsp step %d %s messages length = %d, want 1", step, action.kind, len(messages))
		}
		if messages[0].Method != "textDocument/publishDiagnostics" {
			rt.Fatalf("lsp step %d %s message method = %q, want textDocument/publishDiagnostics", step, action.kind, messages[0].Method)
		}
		params, ok := messages[0].Params.(map[string]any)
		if !ok || params["uri"] != action.uri {
			rt.Fatalf("lsp step %d %s diagnostic params = %#v, want uri %q", step, action.kind, messages[0].Params, action.uri)
		}
	case "initialized", "exit":
		if len(messages) != 0 {
			rt.Fatalf("lsp step %d %s messages length = %d, want 0", step, action.kind, len(messages))
		}
	default:
		if len(messages) != 1 {
			rt.Fatalf("lsp step %d %s messages length = %d, want 1", step, action.kind, len(messages))
		}
		if !jsonRawMessageEqual(messages[0].ID, incoming.ID) {
			rt.Fatalf("lsp step %d %s response ID = %s, want %s", step, action.kind, formatJSONRawMessage(messages[0].ID), formatJSONRawMessage(incoming.ID))
		}
	}
}

func drawRapidLSPIdentifier(rt *rapid.T, label string) string {
	return rapid.StringMatching(`[a-z][a-z0-9_]{0,8}`).Draw(rt, label)
}

func drawRapidLSPSource(rt *rapid.T) string {
	switch rapid.IntRange(0, 3).Draw(rt, "lsp source kind") {
	case 0:
		return drawRapidVibeProgram(rt)
	case 1:
		return fmt.Sprintf("def %s()\n  return %d\nend\n", drawRapidLSPIdentifier(rt, "lsp function"), rapid.IntRange(-100, 100).Draw(rt, "lsp int"))
	case 2:
		return "def broken(\n  1\nend\n"
	default:
		return rapid.StringMatching(`[A-Za-z0-9_ \n().:+\-]{0,120}`).Draw(rt, "lsp arbitrary source")
	}
}

func mustMarshalRapidJSON(value any) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}

func jsonRawMessageEqual(left, right *json.RawMessage) bool {
	if left == nil || right == nil {
		return left == right
	}
	return string(*left) == string(*right)
}

func formatJSONRawMessage(raw *json.RawMessage) string {
	if raw == nil {
		return "<nil>"
	}
	return string(*raw)
}
