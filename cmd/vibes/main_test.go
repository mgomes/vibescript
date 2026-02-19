package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIHelp(t *testing.T) {
	if err := runCLI([]string{"vibes", "help"}); err != nil {
		t.Fatalf("runCLI help failed: %v", err)
	}
}

func TestRunCLIInvalidCommand(t *testing.T) {
	err := runCLI([]string{"vibes", "unknown"})
	if err == nil {
		t.Fatalf("expected invalid command error")
	}
	if !strings.Contains(err.Error(), "invalid command") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCLIWithoutCommand(t *testing.T) {
	err := runCLI([]string{"vibes"})
	if err == nil {
		t.Fatalf("expected invalid command error")
	}
	if !strings.Contains(err.Error(), "invalid command") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCommandCheckOnly(t *testing.T) {
	scriptPath := writeScript(t, `def run
  "ok"
end`)

	if err := runCommand([]string{"-check", scriptPath}); err != nil {
		t.Fatalf("runCommand check failed: %v", err)
	}
}

func TestRunCommandExecutesFunctionAndPrintsResult(t *testing.T) {
	scriptPath := writeScript(t, `def greet(name)
  name
end`)

	out, err := captureStdout(t, func() error {
		return runCommand([]string{"-function", "greet", scriptPath, "hello"})
	})
	if err != nil {
		t.Fatalf("runCommand failed: %v", err)
	}
	if got := strings.TrimSpace(out); got != "hello" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestRunCommandRequiresScriptPath(t *testing.T) {
	err := runCommand(nil)
	if err == nil {
		t.Fatalf("expected script path error")
	}
	if !strings.Contains(err.Error(), "script path required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnalyzeCommandNoIssues(t *testing.T) {
	scriptPath := writeScript(t, `def run()
  value = 1
  value
end`)

	out, err := captureStdout(t, func() error {
		return analyzeCommand([]string{scriptPath})
	})
	if err != nil {
		t.Fatalf("analyzeCommand failed: %v", err)
	}
	if !strings.Contains(out, "No issues found") {
		t.Fatalf("unexpected analyze output: %q", out)
	}
}

func TestAnalyzeCommandReportsUnreachableStatements(t *testing.T) {
	scriptPath := writeScript(t, `def run()
  return 1
  2
end`)

	out, err := captureStdout(t, func() error {
		return analyzeCommand([]string{scriptPath})
	})
	if err == nil {
		t.Fatalf("expected analyze command to report lint failures")
	}
	if !strings.Contains(err.Error(), "analysis found 1 issue(s)") {
		t.Fatalf("unexpected analyze error: %v", err)
	}
	if !strings.Contains(out, "unreachable statement") {
		t.Fatalf("expected unreachable statement warning, got %q", out)
	}
}

func TestComputeModulePathsIncludesScriptDirAndDedupesExtras(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "main.vibe")
	extraDir := t.TempDir()

	dirs, err := computeModulePaths(scriptPath, []string{scriptDir, extraDir, extraDir})
	if err != nil {
		t.Fatalf("computeModulePaths failed: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 dirs, got %d (%v)", len(dirs), dirs)
	}

	wantScript, _ := filepath.Abs(scriptDir)
	wantExtra, _ := filepath.Abs(extraDir)
	if dirs[0] != wantScript {
		t.Fatalf("expected first dir %q, got %q", wantScript, dirs[0])
	}
	if dirs[1] != wantExtra {
		t.Fatalf("expected second dir %q, got %q", wantExtra, dirs[1])
	}
}

func TestComputeModulePathsRejectsNonDirectoryExtra(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "main.vibe")
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	_, err := computeModulePaths(scriptPath, []string{file})
	if err == nil {
		t.Fatalf("expected non-directory module path error")
	}
	if !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func writeScript(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.vibe")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	runErr := fn()
	_ = w.Close()
	os.Stdout = orig

	var buf bytes.Buffer
	if _, copyErr := io.Copy(&buf, r); copyErr != nil {
		t.Fatalf("read stdout: %v", copyErr)
	}
	_ = r.Close()
	return buf.String(), runErr
}
