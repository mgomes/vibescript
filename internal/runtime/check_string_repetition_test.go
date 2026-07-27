package runtime

import (
	"strings"
	"testing"
)

// String#* works at runtime, but the checker's operator matrix had no string
// case, so `vibes check` rejected valid programs. The runtime tests for the
// feature all went through Call and never exercised the static path, which is
// how this got through.
func TestCheckerAcceptsStringRepetition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{name: "literal count", source: `def run()` + "\n  \"-\" * 12\nend"},
		{name: "variable count", source: "def run()\n  w = 5\n  \"=\" * w\nend"},
		{name: "annotated int parameter", source: "def sep(n: int) -> string\n  \"-\" * n\nend"},
		{name: "float count", source: "def run()\n  \"ab\" * 2.0\nend"},
		{name: "annotated string receiver", source: "def rep(s: string, n: int) -> string\n  s * n\nend"},
		{name: "result is a string", source: "def takes(s: string)\n  s\nend\ndef run()\n  takes(\"-\" * 3)\nend"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireNoCheckWarnings(t, script)
		})
	}
}

// Repetition is not commutative, so the reversed form stays rejected.
func TestCheckerRejectsReversedStringRepetition(t *testing.T) {
	t.Parallel()
	script := compileScript(t, "def run()\n  3 * \"ab\"\nend")
	requireCheckWarningContains(t, script, "unsupported multiplication operands")
}

// The result is known to be a string, so a downstream mismatch is still
// reported rather than the whole expression going unknown.
func TestStringRepetitionResultTypeIsKnown(t *testing.T) {
	t.Parallel()
	script := compileScript(t, "def takes_int(n: int)\n  n\nend\ndef run()\n  takes_int(\"-\" * 3)\nend")
	var found bool
	for _, warning := range script.CheckWarnings() {
		if strings.Contains(warning.Message, "expected int, got string") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("repetition result was not known to be a string: %v", script.CheckWarnings())
	}
}
