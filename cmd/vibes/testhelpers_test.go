package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// stdoutMu serializes access to os.Stdout swaps so concurrent helpers cannot
// race the global. Tests that go through captureStdout/captureStdoutErr take
// this lock; tests that do not touch stdout may run in parallel safely.
var stdoutMu sync.Mutex

// newTestCLI returns a fresh tempdir for a CLI test. The directory is cleaned
// up automatically by testing.T. The current working directory is left
// untouched so tests remain safe under t.Parallel.
func newTestCLI(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// writeVibeScript creates a .vibe file under a tempdir and returns its
// absolute path.
func writeVibeScript(t *testing.T, source string) string {
	t.Helper()
	return writeVibeScriptNamed(t, "script.vibe", source)
}

// writeVibeScriptNamed creates a file with the given basename under a fresh
// tempdir and returns its absolute path.
func writeVibeScriptNamed(t *testing.T, name, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// captureStdout runs fn while redirecting os.Stdout into a pipe and returns
// the captured output together with fn's error. The os.Stdout swap is
// serialized via stdoutMu so callers can opt into t.Parallel without racing
// on the global writer.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	stdoutMu.Lock()
	defer stdoutMu.Unlock()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = orig
	}()

	runErr := fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	os.Stdout = orig

	var buf bytes.Buffer
	if _, copyErr := io.Copy(&buf, r); copyErr != nil {
		t.Fatalf("read stdout: %v", copyErr)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return buf.String(), runErr
}

// dispatchCLI invokes runCLI with the supplied args (the program name "vibes"
// is prepended automatically) while capturing stdout. The CLI does not write
// to stderr today; this helper returns it as a convenience for future tests.
func dispatchCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	full := append([]string{"vibes"}, args...)
	out, runErr := captureStdout(t, func() error {
		return runCLI(full)
	})
	return out, "", runErr
}
