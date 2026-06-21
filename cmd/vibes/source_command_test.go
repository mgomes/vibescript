package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// defaultEngineSourceLimit is the engine's default MaxSourceBytes. The CLI
// commands construct engines with the default configuration, so an oversized
// fixture only needs to exceed this value by one byte.
const defaultEngineSourceLimit = 1 << 20

func writeOversizedScript(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	// One byte over the default limit is enough to trigger rejection while the
	// payload stays valid Vibescript (a comment) so any accidental read would
	// still compile and mask the bug we are guarding against.
	payload := "#" + strings.Repeat("a", defaultEngineSourceLimit)
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write oversized script: %v", err)
	}
	return path
}

func TestRunCommandRejectsOversizedScript(t *testing.T) {
	t.Parallel()
	path := writeOversizedScript(t, "script.vibe")

	_, err := captureStdout(t, func() error {
		return runCommand([]string{path})
	})
	if err == nil {
		t.Fatalf("runCommand(%q) err = nil, want source-size rejection", path)
	}
	for _, want := range []string{"read script", "source exceeds maximum size"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("runCommand(%q) err = %v, want substring %q", path, err, want)
		}
	}
}

func TestExecuteScriptRejectsBeforeReadingFile(t *testing.T) {
	t.Parallel()
	path := writeOversizedScript(t, "script.vibe")
	moduleDirs, err := computeModulePaths(filepath.Dir(path), nil)
	if err != nil {
		t.Fatalf("computeModulePaths: %v", err)
	}
	inv := runInvocation{scriptPath: path, function: "run", moduleDirs: moduleDirs}

	_, err = captureStdout(t, func() error {
		return executeScript(context.Background(), inv, os.Stdout)
	})
	if err == nil {
		t.Fatalf("executeScript(%q) err = nil, want source-size rejection", path)
	}
	if want := "source exceeds maximum size"; !strings.Contains(err.Error(), want) {
		t.Fatalf("executeScript(%q) err = %v, want substring %q", path, err, want)
	}
}

func TestAnalyzeCommandRejectsOversizedScript(t *testing.T) {
	t.Parallel()
	path := writeOversizedScript(t, "script.vibe")

	_, err := captureStdout(t, func() error {
		return analyzeCommand([]string{path})
	})
	if err == nil {
		t.Fatalf("analyzeCommand(%q) err = nil, want source-size rejection", path)
	}
	for _, want := range []string{"read script", "source exceeds maximum size"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("analyzeCommand(%q) err = %v, want substring %q", path, err, want)
		}
	}
}

func TestTestCommandRejectsOversizedScript(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "huge_test.vibe")
	payload := "#" + strings.Repeat("a", defaultEngineSourceLimit)
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write oversized test file: %v", err)
	}

	out, err := captureStdout(t, func() error {
		return testCommand([]string{path})
	})
	if err == nil {
		t.Fatalf("testCommand(%q) err = nil, want failure", path)
	}
	for _, want := range []string{"--- FAIL", "(read)", "source exceeds maximum size"} {
		if !strings.Contains(out, want) {
			t.Fatalf("testCommand(%q) output = %q, want substring %q", path, out, want)
		}
	}
}
