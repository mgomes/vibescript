package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/value"
)

func FuzzFormatVibeSource(f *testing.F) {
	for _, seed := range []string{
		"",
		"def run()  \n  1\t \nend",
		"def run()\r\n  1\r\nend\r\n",
		"\t \r\n",
		"# comment\n\n",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, source string) {
		source = limitFormatFuzzString(source, 4096)
		formatted := formatVibeSource(source)

		if !strings.HasSuffix(formatted, "\n") {
			t.Fatalf("formatVibeSource(%q) = %q, want trailing newline", source, formatted)
		}
		if strings.Contains(formatted, "\r") {
			t.Fatalf("formatVibeSource(%q) = %q, want no carriage returns", source, formatted)
		}

		lines := strings.Split(strings.TrimSuffix(formatted, "\n"), "\n")
		for i, line := range lines {
			if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
				t.Fatalf("formatVibeSource(%q) line %d = %q, want no trailing horizontal whitespace", source, i+1, line)
			}
		}

		if second := formatVibeSource(formatted); second != formatted {
			t.Fatalf("formatVibeSource is not idempotent: first %q, second %q", formatted, second)
		}
	})
}

func limitFormatFuzzString(raw string, limit int) string {
	if len(raw) <= limit {
		return raw
	}
	return raw[:limit]
}

func FuzzCLIArgumentAndPathInputs(f *testing.F) {
	f.Add(0, "", "")
	f.Add(1, "run", "greet")
	f.Add(2, "-badflag", "module")
	f.Add(3, "analyze", "nested/path")
	f.Add(4, string([]byte{0xff, 0xfe, 0xfd}), "arg")

	f.Fuzz(func(t *testing.T, selector int, rawArg, rawExtra string) {
		rawArg = limitFormatFuzzString(rawArg, 256)
		rawExtra = limitFormatFuzzString(rawExtra, 256)

		root := t.TempDir()
		scriptPath := filepath.Join(root, "script.vibe")
		if err := os.WriteFile(scriptPath, []byte("def run()\n  1\nend\n"), 0o644); err != nil {
			t.Fatalf("write script: %v", err)
		}
		moduleDir := filepath.Join(root, "modules")
		if err := os.Mkdir(moduleDir, 0o755); err != nil {
			t.Fatalf("mkdir module dir: %v", err)
		}
		notDir := filepath.Join(root, "not-dir")
		if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
			t.Fatalf("write non-directory: %v", err)
		}

		var modulePaths pathList
		if err := modulePaths.Set(rawArg); err != nil {
			t.Fatalf("pathList.Set(%q) = %v, want nil", rawArg, err)
		}
		_ = modulePaths.String()

		extras := [][]string{
			nil,
			{moduleDir},
			{moduleDir, moduleDir},
			{notDir},
			{filepath.Join(root, rawExtra)},
		}
		selectedExtras := extras[positiveMod(selector, len(extras))]
		dirs, err := computeModulePaths(scriptPath, selectedExtras)
		if err == nil {
			seen := make(map[string]struct{}, len(dirs))
			for _, dir := range dirs {
				if !filepath.IsAbs(dir) {
					t.Fatalf("computeModulePaths(%q, %v) returned non-absolute path %q", scriptPath, selectedExtras, dir)
				}
				info, statErr := os.Stat(dir)
				if statErr != nil {
					t.Fatalf("computeModulePaths(%q, %v) returned inaccessible path %q: %v", scriptPath, selectedExtras, dir, statErr)
				}
				if !info.IsDir() {
					t.Fatalf("computeModulePaths(%q, %v) returned non-directory %q", scriptPath, selectedExtras, dir)
				}
				if _, ok := seen[dir]; ok {
					t.Fatalf("computeModulePaths(%q, %v) returned duplicate path %q", scriptPath, selectedExtras, dir)
				}
				seen[dir] = struct{}{}
			}
		}

		switch positiveMod(selector, 6) {
		case 0:
			_ = runCLI([]string{"vibes"})
			_ = runCLI([]string{"vibes", rawArg})
			_ = runCLI([]string{"vibes", "help"})
		case 1:
			_ = runCommand([]string{"-check", "-function", rawArg, "-module-path", moduleDir, scriptPath, rawExtra})
		case 2:
			_ = runCommand([]string{"-check", rawArg})
		case 3:
			_, _ = captureStdout(t, func() error {
				return analyzeCommand([]string{scriptPath})
			})
		case 4:
			_ = analyzeCommand([]string{rawArg})
		default:
			_ = fmtCommand([]string{"-check", scriptPath})
		}
	})
}

func FuzzREPLInputFlow(f *testing.F) {
	for _, seed := range []string{
		"",
		"1 + 2",
		"score = 42",
		":help",
		":unknown command",
		"def broken(",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		input = limitFormatFuzzString(input, 512)

		model, err := newREPLModel()
		if err != nil {
			t.Fatalf("newREPLModel failed: %v", err)
		}
		model.env["seed"] = value.NewString(input)

		commandInput := ":" + strings.TrimSpace(strings.TrimPrefix(input, ":"))
		model, cmd := model.handleCommand(commandInput)
		if cmd != nil {
			_ = cmd()
		}

		model.textInput.SetValue(input)
		model = model.handleAutocomplete()

		output, isErr := model.evaluate(input)
		if isErr && model.lastError == "" {
			t.Fatalf("replModel.evaluate(%q) returned an error without setting lastError", input)
		}
		if isErr && output == "" {
			t.Fatalf("replModel.evaluate(%q) returned an empty error", input)
		}
		if !isErr {
			if _, ok := model.env["_"]; !ok {
				t.Fatalf("replModel.evaluate(%q) succeeded without storing underscore result", input)
			}
		}

		updated, updateCmd := model.Update(tea.WindowSizeMsg{Width: len(input) % 120, Height: len(input) % 40})
		repl, ok := updated.(replModel)
		if !ok {
			t.Fatalf("replModel.Update(WindowSizeMsg) returned %T, want replModel", updated)
		}
		if updateCmd != nil {
			_ = updateCmd()
		}
		_ = repl.View()

		repl.textInput.SetValue(input)
		updated, updateCmd = repl.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if _, ok := updated.(replModel); !ok {
			t.Fatalf("replModel.Update(KeyEnter) returned %T, want replModel", updated)
		}
		if updateCmd != nil {
			_ = updateCmd()
		}
	})
}

func FuzzLSPPayloadAndMessageHandling(f *testing.F) {
	f.Add(0, "def run()\n  1\nend\n", "file:///tmp/test.vibe")
	f.Add(1, "def run(\n  1\nend\n", "file:///tmp/broken.vibe")
	f.Add(2, "assert true", "")
	f.Add(3, string([]byte{0xff, 0xfe, 0xfd}), "not a uri")

	f.Fuzz(func(t *testing.T, selector int, source, uri string) {
		source = limitFormatFuzzString(source, 2048)
		uri = limitFormatFuzzString(uri, 512)

		server := &lspServer{
			engine: vibes.MustNewEngine(vibes.Config{}),
			docs:   map[string]string{uri: source},
		}

		for _, diag := range diagnosticsForSource(server.engine, source) {
			if diag["severity"] != 1 {
				t.Fatalf("diagnosticsForSource(%q) severity = %#v, want 1", source, diag["severity"])
			}
			message, ok := diag["message"].(string)
			if !ok || message == "" {
				t.Fatalf("diagnosticsForSource(%q) message = %#v, want non-empty string", source, diag["message"])
			}
		}

		word := wordAtPosition(source, positiveMod(selector, 16)-4, positiveMod(len(uri)+selector, 128)-8)
		if strings.ContainsAny(word, " \t\r\n") {
			t.Fatalf("wordAtPosition(%q) = %q, want a single word", source, word)
		}
		_ = classifyWord(word)

		incoming := fuzzLSPMessage(selector, uri, source)
		messages := server.handleMessage(incoming)
		for _, msg := range messages {
			if msg.JSONRPC == "" {
				t.Fatalf("lspServer.handleMessage(%q) returned message without JSONRPC version", incoming.Method)
			}
			if _, err := json.Marshal(msg); err != nil {
				t.Fatalf("json.Marshal(lsp outbound %q) failed: %v", incoming.Method, err)
			}
		}

		var out bytes.Buffer
		server.writer = bufio.NewWriter(&out)
		if len(messages) > 0 {
			if err := server.writePayload(messages[0]); err != nil {
				t.Fatalf("lspServer.writePayload(%q) failed: %v", incoming.Method, err)
			}
			if !strings.HasPrefix(out.String(), "Content-Length: ") {
				t.Fatalf("lspServer.writePayload(%q) wrote malformed payload %q", incoming.Method, out.String())
			}
		}

		payload := []byte(limitFormatFuzzString(source, 512))
		wire := "Noise: ignored\r\nContent-Length: " + intString(len(payload)) + "\r\n\r\n" + string(payload)
		server.reader = bufio.NewReader(strings.NewReader(wire))
		gotPayload, err := server.readPayload()
		if err != nil {
			t.Fatalf("lspServer.readPayload(valid payload) failed: %v", err)
		}
		if !bytes.Equal(gotPayload, payload) {
			t.Fatalf("lspServer.readPayload() = %q, want %q", string(gotPayload), string(payload))
		}

		badWire := "Content-Length: " + limitFormatFuzzString(uri, 64) + "\r\n\r\n"
		server.reader = bufio.NewReader(strings.NewReader(badWire))
		_, _ = server.readPayload()

		oversizedWire := "Content-Length: " + intString(maxLSPPayloadBytes+1) + "\r\n\r\n"
		server.reader = bufio.NewReader(strings.NewReader(oversizedWire))
		if _, err := server.readPayload(); err == nil {
			t.Fatalf("lspServer.readPayload(oversized payload) = nil error, want max size error")
		}
	})
}

func fuzzLSPMessage(selector int, uri, source string) lspInboundMessage {
	idRaw := json.RawMessage(`"fuzz"`)
	switch positiveMod(selector, 7) {
	case 0:
		return lspInboundMessage{JSONRPC: "2.0", ID: &idRaw, Method: "initialize"}
	case 1:
		params := mustMarshalFuzzJSON(map[string]any{
			"textDocument": map[string]any{"uri": uri, "text": source},
		})
		return lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: params}
	case 2:
		params := mustMarshalFuzzJSON(map[string]any{
			"textDocument":   map[string]any{"uri": uri},
			"contentChanges": []map[string]any{{"text": source}, {"text": source + "\n"}},
		})
		return lspInboundMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: params}
	case 3:
		return lspInboundMessage{JSONRPC: "2.0", ID: &idRaw, Method: "textDocument/completion"}
	case 4:
		params := mustMarshalFuzzJSON(map[string]any{
			"textDocument": map[string]any{"uri": uri},
			"position":     map[string]any{"line": positiveMod(selector, 8), "character": positiveMod(len(source), 32)},
		})
		return lspInboundMessage{JSONRPC: "2.0", ID: &idRaw, Method: "textDocument/hover", Params: params}
	case 5:
		return lspInboundMessage{JSONRPC: "2.0", ID: &idRaw, Method: "shutdown"}
	default:
		return lspInboundMessage{JSONRPC: "2.0", ID: &idRaw, Method: uri}
	}
}

func mustMarshalFuzzJSON(value any) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}

func positiveMod(n, mod int) int {
	if mod <= 0 {
		return 0
	}
	n %= mod
	if n < 0 {
		n += mod
	}
	return n
}

func intString(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
