package parser

import (
	"strings"
	"testing"
)

// `name:` followed by a bare identifier parses as a type annotation, which
// makes the parameter ordinary, which trips the ordering rule -- so writing the
// documented "a later default may reference an earlier parameter" feature in
// its simplest form reported parameter ordering instead. The boundary is
// invisible: `b: a * 1` and `b: (a)` are defaults and only bare `b: a` is not,
// and the ordering message gives no hint that a parenthesis is the fix.
func TestBareIdentifierKeywordDefaultExplainsItself(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{name: "keyword parameter reference", source: "def f(a:, b: a)\n  1\nend"},
		{name: "later parameter in a longer list", source: "def f(host:, port: 8080, timeout: port)\n  1\nend"},
		{name: "ordinary parameter reference", source: "def f(a:, b:, c: a)\n  1\nend"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, tc.source)
			if len(errs) == 0 {
				t.Fatalf("%s: expected a parse error", tc.source)
			}
			joined := joinParseErrors(errs)
			if !strings.Contains(joined, "reads as a type annotation, not a default") {
				t.Fatalf("%s: error = %s, want it to explain the type-annotation reading", tc.source, joined)
			}
			if !strings.Contains(joined, "to default") {
				t.Fatalf("%s: error = %s, want it to show the parenthesised form", tc.source, joined)
			}
		})
	}
}

// The parenthesised form is the fix the diagnostic points at, so it has to
// work.
func TestParenthesisedKeywordDefaultParses(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		"def f(a:, b: (a))\n  1\nend",
		"def f(host:, port: 8080, timeout: (port))\n  1\nend",
		"def f(a:, b: a * 1)\n  1\nend",
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			if _, errs := parseSource(t, source); len(errs) != 0 {
				t.Fatalf("%s: unexpected parse errors: %v", source, errs)
			}
		})
	}
}

// A genuine ordering mistake keeps the ordering message: the specific
// diagnostic must not swallow the general one.
func TestGenuineParameterOrderingKeepsItsMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{name: "ordinary after rest", source: "def f(*rest, b)\n  1\nend"},
		{name: "ordinary after keyword", source: "def f(a:, b)\n  1\nend"},
		// A builtin type name is a type beyond reasonable doubt even when a
		// parameter shadows it, so it keeps the ordering message.
		{name: "builtin type name shadowed by a parameter", source: "def f(int:, b: int)\n  1\nend"},
		// A name that matches no earlier parameter is an ordinary annotation.
		{name: "unrelated type name", source: "def f(a:, b: SomeClass)\n  1\nend"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, tc.source)
			if len(errs) == 0 {
				t.Fatalf("%s: expected a parse error", tc.source)
			}
			joined := joinParseErrors(errs)
			if !strings.Contains(joined, "ordinary parameters must precede") {
				t.Fatalf("%s: error = %s, want the ordering message", tc.source, joined)
			}
		})
	}
}

func joinParseErrors(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, "\n")
}
