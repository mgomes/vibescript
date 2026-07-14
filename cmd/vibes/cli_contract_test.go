package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

const (
	cliContractHelperEnv = "VIBES_CLI_CONTRACT_HELPER"
	cliContractArgsEnv   = "VIBES_CLI_CONTRACT_ARGS"
)

const cliContractUsage = `Usage: vibes <command> [flags] [args...]

Commands:
  run <script>    Execute a script file
  check <script>  Static contract checking without executing
  fmt <path>      Canonical formatting for .vibe files
  analyze <script> Analyze a script for lint issues
  test [path...]  Run *_test.vibe files (-run <regexp> to filter)
  lsp             Start language server (stdio)
  repl            Start interactive REPL
  help            Show this help message

Run flags:
  -function string
    function to invoke after compilation (default "run")
  -check
    compile and validate static contracts without executing
  -e <snippet>
    evaluate an inline snippet instead of a script file
  -watch
    re-run whenever the script or its modules change
  -module-path <dir>
    add a directory to module search paths (repeatable)
  -profile <name>
    execution quota profile: low, medium, high, xhigh (default "xhigh")
  -step-quota / -memory-quota / -recursion-limit <n>
    override a profile quota (-1 = unlimited)
`

type cliContractResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

func TestCLIContractHelperProcess(t *testing.T) {
	if os.Getenv(cliContractHelperEnv) != "1" {
		return
	}

	var args []string
	if err := json.Unmarshal([]byte(os.Getenv(cliContractArgsEnv)), &args); err != nil {
		fmt.Fprintf(os.Stderr, "decode CLI contract arguments: %v\n", err)
		os.Exit(2)
	}
	os.Args = append([]string{"vibes"}, args...)
	main()
	os.Exit(0)
}

func TestCLIContractRootDispatch(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want cliContractResult
	}{
		{
			name: "help",
			args: []string{"--help"},
			want: cliContractResult{Stderr: cliContractUsage},
		},
		{
			name: "missing command",
			want: cliContractResult{
				ExitCode: 1,
				Stderr:   cliContractUsage + "invalid command\n",
			},
		},
		{
			name: "unknown command",
			args: []string{"unknown"},
			want: cliContractResult{
				ExitCode: 1,
				Stderr:   cliContractUsage + "invalid command\n",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runCLIContractProcess(t, "", tc.args...)
			assertCLIContractResult(t, got, tc.want)
		})
	}
}

func TestCLIContractSubcommandFlagErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "help",
			args: []string{"run", "-h"},
			want: "flag: help requested\n",
		},
		{
			name: "unknown flag",
			args: []string{"run", "-unknown"},
			want: "flag provided but not defined: -unknown\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runCLIContractProcess(t, "", tc.args...)
			assertCLIContractResult(t, got, cliContractResult{
				ExitCode: 1,
				Stderr:   tc.want,
			})
		})
	}
}

func TestCLIContractRunStopsParsingAtFirstPositional(t *testing.T) {
	scriptPath := writeVibeScript(t, `def run(value)
  value
end
`)

	got := runCLIContractProcess(t, "", "run", scriptPath, "-check")
	assertCLIContractResult(t, got, cliContractResult{Stdout: "-check\n"})
}

func TestCLIContractRunDoubleDashTerminatesFlags(t *testing.T) {
	scriptPath := writeVibeScript(t, `def run(value)
  value
end
`)

	got := runCLIContractProcess(t, "", "run", "--", scriptPath, "-check")
	assertCLIContractResult(t, got, cliContractResult{Stdout: "-check\n"})
}

func TestCLIContractRunExplicitEmptySnippet(t *testing.T) {
	got := runCLIContractProcess(t, "", "run", "-e=")
	assertCLIContractResult(t, got, cliContractResult{
		ExitCode: 1,
		Stderr:   "vibes run: -e requires a non-empty snippet\n",
	})
}

func TestCLIContractRunExplicitDefaultFunction(t *testing.T) {
	scriptPath := writeVibeScript(t, `def run
  "function"
end

"top-level"
`)

	got := runCLIContractProcess(t, "", "run", scriptPath)
	assertCLIContractResult(t, got, cliContractResult{Stdout: "top-level\n"})

	got = runCLIContractProcess(t, "", "run", "-function=run", scriptPath)
	assertCLIContractResult(t, got, cliContractResult{Stdout: "function\n"})
}

func TestCLIContractRunExplicitZeroQuota(t *testing.T) {
	scriptPath := writeVibeScript(t, `def count(n)
  i = 0
  while i < n
    i = i + 1
  end
  i
end

puts count(2000000)
`)

	got := runCLIContractProcess(t, "", "run", scriptPath)
	assertCLIContractResult(t, got, cliContractResult{Stdout: "2000000\n"})

	got = runCLIContractProcess(t, "", "run", "-step-quota=0", scriptPath)
	if got.ExitCode != 1 {
		t.Errorf("explicit zero quota exit code = %d, want 1", got.ExitCode)
	}
	if got.Stdout != "" {
		t.Errorf("explicit zero quota stdout = %q, want empty", got.Stdout)
	}
	if !strings.Contains(got.Stderr, "step quota exceeded (1000000)") {
		t.Errorf("explicit zero quota stderr = %q, want low-profile step quota failure", got.Stderr)
	}
}

func TestCLIContractRunRepeatableModulePaths(t *testing.T) {
	root := t.TempDir()
	firstDir := filepath.Join(root, "first-modules")
	secondDir := filepath.Join(root, "second-modules")
	mainDir := filepath.Join(root, "main")
	writeCLIContractFile(t, firstDir, "first.vibe", `def value
  "first"
end
`)
	writeCLIContractFile(t, secondDir, "second.vibe", `def value
  "second"
end
`)
	mainPath := writeCLIContractFile(t, mainDir, "main.vibe", `first = require("first")
second = require("second")
first.value + ":" + second.value
`)

	got := runCLIContractProcess(t, "",
		"run",
		"-module-path", firstDir,
		"-module-path", secondDir,
		mainPath,
	)
	assertCLIContractResult(t, got, cliContractResult{Stdout: "first:second\n"})
}

func TestCLIContractLSPPreservesStdoutFraming(t *testing.T) {
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	exit := `{"jsonrpc":"2.0","method":"exit"}`
	input := frameCLIContractLSPPayload(initialize) + frameCLIContractLSPPayload(exit)

	got := runCLIContractProcess(t, input, "lsp")
	if got.ExitCode != 0 {
		t.Errorf("lsp exit code = %d, want 0", got.ExitCode)
	}
	if got.Stderr != "" {
		t.Errorf("lsp stderr = %q, want empty", got.Stderr)
	}

	payload := decodeCLIContractLSPFrame(t, got.Stdout)
	var response struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Capabilities map[string]any `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("decode LSP response: %v", err)
	}
	if response.JSONRPC != "2.0" {
		t.Errorf("LSP jsonrpc = %q, want 2.0", response.JSONRPC)
	}
	if response.ID != 1 {
		t.Errorf("LSP response id = %d, want 1", response.ID)
	}
	if len(response.Result.Capabilities) == 0 {
		t.Error("LSP initialize capabilities are empty")
	}
}

func runCLIContractProcess(t *testing.T, stdin string, args ...string) cliContractResult {
	t.Helper()

	encodedArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("encode CLI arguments: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCLIContractHelperProcess$")
	cmd.Env = append(os.Environ(),
		cliContractHelperEnv+"=1",
		cliContractArgsEnv+"="+string(encodedArgs),
	)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("CLI process timed out for arguments %q", args)
	}
	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			t.Fatalf("run CLI process with arguments %q: %v", args, runErr)
		}
		exitCode = exitErr.ExitCode()
	}

	return cliContractResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
}

func assertCLIContractResult(t *testing.T, got, want cliContractResult) {
	t.Helper()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("CLI result mismatch (-want +got):\n%s", diff)
	}
}

func writeCLIContractFile(t *testing.T, dir, name, source string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create directory %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func frameCLIContractLSPPayload(payload string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload)
}

func decodeCLIContractLSPFrame(t *testing.T, output string) []byte {
	t.Helper()
	header, body, ok := strings.Cut(output, "\r\n\r\n")
	if !ok {
		t.Fatalf("LSP stdout = %q, want a framed payload", output)
	}
	const prefix = "Content-Length: "
	if !strings.HasPrefix(header, prefix) {
		t.Fatalf("LSP header = %q, want Content-Length", header)
	}
	contentLength, err := strconv.Atoi(strings.TrimPrefix(header, prefix))
	if err != nil {
		t.Fatalf("parse LSP Content-Length from %q: %v", header, err)
	}
	if len(body) != contentLength {
		t.Fatalf("LSP body length = %d, want %d; stdout = %q", len(body), contentLength, output)
	}
	return []byte(body)
}
