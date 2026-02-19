package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFmtCommandRequiresPath(t *testing.T) {
	err := fmtCommand(nil)
	if err == nil {
		t.Fatalf("expected path required error")
	}
	if !strings.Contains(err.Error(), "path required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFmtCommandCheckDetectsUnformattedFiles(t *testing.T) {
	path := writeVibeFile(t, "def run()  \n  1\t \nend")
	err := fmtCommand([]string{"-check", path})
	if err == nil {
		t.Fatalf("expected formatting check failure")
	}
	if !strings.Contains(err.Error(), "need formatting") {
		t.Fatalf("unexpected check error: %v", err)
	}
}

func TestFmtCommandWriteFormatsFileInPlace(t *testing.T) {
	path := writeVibeFile(t, "def run()  \n  1\t \nend")
	if err := fmtCommand([]string{"-w", path}); err != nil {
		t.Fatalf("fmt -w failed: %v", err)
	}

	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read formatted file: %v", err)
	}
	if got := string(updated); got != "def run()\n  1\nend\n" {
		t.Fatalf("unexpected formatted output: %q", got)
	}
}

func TestFmtCommandPrintsFormattedOutput(t *testing.T) {
	path := writeVibeFile(t, "def run()  \n  1\t \nend")
	out, err := captureStdout(t, func() error {
		return fmtCommand([]string{path})
	})
	if err != nil {
		t.Fatalf("fmt command failed: %v", err)
	}
	if out != "def run()\n  1\nend\n" {
		t.Fatalf("unexpected stdout output: %q", out)
	}
}

func TestFmtCommandFormatsDirectories(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "a.vibe")
	second := filepath.Join(root, "nested", "b.vibe")
	if err := os.MkdirAll(filepath.Dir(second), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(first, []byte("def run()  \n  1  \nend"), 0o644); err != nil {
		t.Fatalf("write first file: %v", err)
	}
	if err := os.WriteFile(second, []byte("def run()  \n  2\t\nend"), 0o644); err != nil {
		t.Fatalf("write second file: %v", err)
	}

	if err := fmtCommand([]string{"-w", root}); err != nil {
		t.Fatalf("fmt directory failed: %v", err)
	}
	if err := fmtCommand([]string{"-check", root}); err != nil {
		t.Fatalf("expected no formatting diffs after write, got %v", err)
	}
}

func writeVibeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.vibe")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write vibe file: %v", err)
	}
	return path
}
