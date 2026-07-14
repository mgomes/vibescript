package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// stdoutMu serializes the capture helpers with each other. Tests using them
// must still remain sequential because production code can read the process
// streams without taking this test-only lock.
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
// serialized via stdoutMu. Callers must remain sequential because other
// parallel tests may read the process-wide stream directly.
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

// captureStdoutErr runs fn while redirecting both process output streams to
// temporary files. Files avoid pipe-buffer deadlocks when generated help or a
// command diagnostic grows beyond a pipe's capacity.
func captureStdoutErr(t *testing.T, fn func() error) (stdout, stderr string, runErr error) {
	t.Helper()

	stdoutMu.Lock()
	defer stdoutMu.Unlock()

	stdoutFile, err := os.CreateTemp(t.TempDir(), "stdout-")
	if err != nil {
		t.Fatalf("create stdout capture: %v", err)
	}
	defer func() {
		_ = stdoutFile.Close()
	}()
	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr-")
	if err != nil {
		t.Fatalf("create stderr capture: %v", err)
	}
	defer func() {
		_ = stderrFile.Close()
	}()
	originalStdout := os.Stdout
	originalStderr := os.Stderr
	os.Stdout = stdoutFile
	os.Stderr = stderrFile
	defer func() {
		os.Stdout = originalStdout
		os.Stderr = originalStderr
	}()

	runErr = fn()
	os.Stdout = originalStdout
	os.Stderr = originalStderr
	if err := stdoutFile.Close(); err != nil {
		t.Fatalf("close stdout capture: %v", err)
	}
	if err := stderrFile.Close(); err != nil {
		t.Fatalf("close stderr capture: %v", err)
	}
	stdoutBytes, err := os.ReadFile(stdoutFile.Name())
	if err != nil {
		t.Fatalf("read stdout capture: %v", err)
	}
	stderrBytes, err := os.ReadFile(stderrFile.Name())
	if err != nil {
		t.Fatalf("read stderr capture: %v", err)
	}
	return string(stdoutBytes), string(stderrBytes), runErr
}

// dispatchCLI invokes runCLI with the supplied args (the program name "vibes"
// is prepended automatically) while capturing both output streams.
func dispatchCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	full := append([]string{"vibes"}, args...)
	return captureStdoutErr(t, func() error {
		return runCLI(full)
	})
}
