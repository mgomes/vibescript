package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes"
)

func newTestBuiltinCatalog(t testing.TB) builtinCatalog {
	t.Helper()
	engine, err := vibes.NewEngine(vibes.Config{})
	if err != nil {
		t.Fatalf("vibes.NewEngine() failed: %v", err)
	}
	return newBuiltinCatalog(engine.Builtins())
}

func testCompletionItems(t testing.TB) []map[string]any {
	t.Helper()
	return buildCompletionItems(newTestBuiltinCatalog(t))
}

func testBuiltinSignature(t testing.TB, name string) string {
	t.Helper()
	entry, ok := builtinDocs()[name]
	if !ok {
		t.Fatalf("builtinDocs() missing %q", name)
	}
	return strings.ReplaceAll(entry.Signature, "`", "")
}

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

// dispatchCLI runs the full command tree with the supplied args while
// capturing both output streams.
func dispatchCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	full := append([]string{"vibes"}, args...)
	var stdoutBuffer bytes.Buffer
	var stderrBuffer bytes.Buffer
	err = runCLIContextWithIO(t.Context(), full, strings.NewReader(""), &stdoutBuffer, &stderrBuffer)
	return stdoutBuffer.String(), stderrBuffer.String(), err
}

func dispatchCommand(t *testing.T, name string, args []string) (stdout string, err error) {
	t.Helper()
	commandArgs := make([]string, 0, 1+len(args))
	commandArgs = append(commandArgs, name)
	commandArgs = append(commandArgs, args...)
	stdout, _, err = dispatchCLI(t, commandArgs...)
	return stdout, err
}
