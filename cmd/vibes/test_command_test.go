package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestVibeFile(t *testing.T, dir, name, source string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestTestCommandPassingAndFailingTests(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestVibeFile(t, dir, "math_test.vibe", `def test_addition()
  assert 1 + 2 == 3
end

def test_subtraction()
  assert 5 - 2 == 3, "subtraction is broken"
end

def helper()
  "not a test"
end
`)
	writeTestVibeFile(t, dir, "broken_test.vibe", `def test_failure()
  assert 1 == 2, "one is not two"
end
`)

	out, err := captureStdout(t, func() error {
		return testCommand([]string{dir})
	})
	if err == nil {
		t.Fatalf("testCommand(%q) err = nil, want failure", dir)
	}
	if !strings.Contains(err.Error(), "1 test(s) failed") {
		t.Fatalf("testCommand err = %v, want 1 failed test", err)
	}
	for _, want := range []string{
		"--- FAIL: " + filepath.Join(dir, "broken_test.vibe") + " :: test_failure",
		"one is not two",
		"ok   " + filepath.Join(dir, "math_test.vibe") + " (2 test(s))",
		"3 test(s) across 2 file(s): 2 passed, 1 failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("testCommand output = %q, want substring %q", out, want)
		}
	}
	if strings.Contains(out, "helper") {
		t.Fatalf("testCommand output mentions non-test function: %q", out)
	}
}

func TestTestCommandAllPassing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestVibeFile(t, dir, "ok_test.vibe", `def test_truth()
  assert true
end
`)

	out, err := captureStdout(t, func() error {
		return testCommand([]string{dir})
	})
	if err != nil {
		t.Fatalf("testCommand(%q) err = %v, want nil", dir, err)
	}
	if !strings.Contains(out, "1 test(s) across 1 file(s): 1 passed, 0 failed") {
		t.Fatalf("testCommand output = %q, want passing summary", out)
	}
}

func TestTestCommandReportsAssertionPosition(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestVibeFile(t, dir, "pos_test.vibe", `def test_position()
  value = 1
  assert value == 2
end
`)

	out, err := captureStdout(t, func() error {
		return testCommand([]string{dir})
	})
	if err == nil {
		t.Fatal("testCommand err = nil, want failure")
	}
	if !strings.Contains(out, "line 3") {
		t.Fatalf("testCommand output = %q, want assertion source position (line 3)", out)
	}
}

func TestTestCommandRunFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestVibeFile(t, dir, "filter_test.vibe", `def test_alpha()
  assert true
end

def test_beta()
  assert false, "beta always fails"
end
`)

	out, err := captureStdout(t, func() error {
		return testCommand([]string{"-run", "alpha", dir})
	})
	if err != nil {
		t.Fatalf("testCommand with -run alpha err = %v, want nil", err)
	}
	if !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("filtered output = %q, want only test_alpha to run", out)
	}

	_, err = captureStdout(t, func() error {
		return testCommand([]string{"-run", "[", dir})
	})
	if err == nil || !strings.Contains(err.Error(), "invalid -run pattern") {
		t.Fatalf("testCommand with bad pattern err = %v, want invalid pattern error", err)
	}
}

func TestTestCommandDiscoversNestedFilesAndModules(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestVibeFile(t, dir, "helper.vibe", `def double(n)
  n * 2
end
`)
	writeTestVibeFile(t, dir, filepath.Join("nested", "deep_test.vibe"), `def test_double()
  helper = require("helper")
  assert helper.double(2) == 4
end
`)

	out, err := captureStdout(t, func() error {
		return testCommand([]string{"-module-path", dir, dir})
	})
	if err != nil {
		t.Fatalf("testCommand(%q) err = %v, want nil\noutput: %s", dir, err, out)
	}
	if !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("nested discovery output = %q, want one passing test", out)
	}
}

func TestTestCommandCompileFailureIsReported(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestVibeFile(t, dir, "bad_test.vibe", "def test_oops(\n")

	out, err := captureStdout(t, func() error {
		return testCommand([]string{dir})
	})
	if err == nil {
		t.Fatal("testCommand err = nil, want compile failure")
	}
	if !strings.Contains(out, "--- FAIL: "+filepath.Join(dir, "bad_test.vibe")+" :: (compile)") {
		t.Fatalf("output = %q, want compile failure entry", out)
	}
}

func TestTestCommandRejectsRequiredParams(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestVibeFile(t, dir, "params_test.vibe", `def test_needs_arg(value)
  assert value
end

def test_default_ok(value = 1)
  assert value == 1
end
`)

	out, err := captureStdout(t, func() error {
		return testCommand([]string{dir})
	})
	if err == nil {
		t.Fatal("testCommand err = nil, want failure for required params")
	}
	for _, want := range []string{
		"test functions must not require parameters",
		"1 passed, 1 failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want substring %q", out, want)
		}
	}
}

func TestTestCommandNoTestFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := captureStdout(t, func() error {
		return testCommand([]string{dir})
	})
	if err == nil || !strings.Contains(err.Error(), "no *_test.vibe files found") {
		t.Fatalf("testCommand on empty dir err = %v, want discovery error", err)
	}
}

func TestTestCommandExplicitFileMustMatchConvention(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plain := writeTestVibeFile(t, dir, "script.vibe", "def run()\n  1\nend\n")

	_, err := captureStdout(t, func() error {
		return testCommand([]string{plain})
	})
	if err == nil || !strings.Contains(err.Error(), "is not a *_test.vibe file") {
		t.Fatalf("testCommand on plain file err = %v, want naming error", err)
	}
}
