package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncWriter is a goroutine-safe buffer for collecting watch output while
// the watch loop runs in a background goroutine.
type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func waitForOutput(t *testing.T, w *syncWriter, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for output %q, got %q", want, w.String())
}

func writeScriptFile(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestWatchScriptRerunsOnChangeAndSurvivesErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "main.vibe")
	writeScriptFile(t, scriptPath, "def run()\n  \"first\"\nend\n")

	inv := runInvocation{
		scriptPath: scriptPath,
		function:   "run",
		moduleDirs: []string{dir},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncWriter{}
	status := &syncWriter{}
	done := make(chan error, 1)
	go func() {
		done <- watchScript(ctx, inv, 10*time.Millisecond, out, status)
	}()

	waitForOutput(t, out, "first")

	writeScriptFile(t, scriptPath, "def run()\n  \"second result\"\nend\n")
	waitForOutput(t, out, "second result")
	waitForOutput(t, status, "change detected")

	writeScriptFile(t, scriptPath, "def run(\n")
	waitForOutput(t, status, "compile failed")

	writeScriptFile(t, scriptPath, "def run()\n  \"recovered output\"\nend\n")
	waitForOutput(t, out, "recovered output")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watchScript returned %v, want nil after cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watchScript did not stop after context cancel")
	}
	if !strings.Contains(status.String(), "watch stopped") {
		t.Fatalf("status = %q, want shutdown notice", status.String())
	}
}

func TestWatchScriptRerunsOnModuleFileChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "main.vibe")
	if err := os.Mkdir(filepath.Join(dir, "billing"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	modulePath := filepath.Join(dir, "billing", "helper.vibe")
	writeScriptFile(t, scriptPath, "def run()\n  \"module watch up\"\nend\n")
	writeScriptFile(t, modulePath, "def helper()\n  1\nend\n")

	inv := runInvocation{
		scriptPath: scriptPath,
		function:   "run",
		moduleDirs: []string{dir},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncWriter{}
	status := &syncWriter{}
	done := make(chan error, 1)
	go func() {
		done <- watchScript(ctx, inv, 10*time.Millisecond, out, status)
	}()

	waitForOutput(t, out, "module watch up")

	writeScriptFile(t, modulePath, "def helper()\n  2\nend\n")
	waitForOutput(t, status, "change detected")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watchScript returned %v, want nil after cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watchScript did not stop after context cancel")
	}
}

func TestSnapshotWatchTargetsStampsVibeFilesRecursively(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "main.vibe")
	writeScriptFile(t, scriptPath, "def run()\n  nil\nend\n")
	writeScriptFile(t, filepath.Join(dir, "helper.vibe"), "def helper()\n  1\nend\n")
	writeScriptFile(t, filepath.Join(dir, "notes.txt"), "ignored")
	if err := os.MkdirAll(filepath.Join(dir, "billing", "deep"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeScriptFile(t, filepath.Join(dir, "billing", "fees.vibe"), "def fees()\n  2\nend\n")
	writeScriptFile(t, filepath.Join(dir, "billing", "deep", "rates.vibe"), "def rates()\n  3\nend\n")
	writeScriptFile(t, filepath.Join(dir, "billing", "readme.md"), "ignored")
	if err := os.Mkdir(filepath.Join(dir, "dir-named.vibe"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	inv := runInvocation{scriptPath: scriptPath, moduleDirs: []string{dir}}
	snapshot := snapshotWatchTargets(inv)

	want := map[string]bool{
		scriptPath:                                          true,
		filepath.Join(dir, "helper.vibe"):                   true,
		filepath.Join(dir, "billing", "fees.vibe"):          true,
		filepath.Join(dir, "billing", "deep", "rates.vibe"): true,
	}
	if len(snapshot) != len(want) {
		t.Fatalf("snapshot has %d entries (%v), want %d", len(snapshot), snapshot, len(want))
	}
	for path := range want {
		if _, ok := snapshot[path]; !ok {
			t.Fatalf("snapshot missing %s: %v", path, snapshot)
		}
	}
}
