package parser

import (
	"strings"
	"testing"
)

// The ampersand block argument turned a call's block into a value: `&blk`
// forwarded a captured block, `&fn` a function, and `&:name` a symbol-to-proc.
// ADR-006 removed all three; the diagnostic must point at writing the block on
// the call that runs it.
func TestParserAmpersandBlockArgumentIsRemoved(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		"def run\n  mapper = nil\n  [1, 2].map(&mapper)\nend",
		"def run\n  [\"a\", \"b\"].map(&:upcase)\nend",
		"def run\n  [1, 2, 3].reduce(&:+)\nend",
		"def run\n  mapper = nil\n  values.map &mapper\nend",
		"def run\n  mapper = nil\n  values.fetch(0, &mapper)\nend",
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, source)
			if len(errs) == 0 {
				t.Fatal("expected a parse error for an ampersand block argument")
			}
			joined := joinTeachingErrors(errs)
			if !strings.Contains(joined, "block arguments are not supported") {
				t.Fatalf("error = %s, want it to name the removal", joined)
			}
		})
	}
}

// The binary intersection operator shares the ampersand token and keeps its
// meaning; only the argument-prefix spacing reads as a block argument.
func TestParserAmpersandStaysAnOperator(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		"def run\n  [1, 2] & [2, 3]\nend",
		"def run\n  locals = [1]\n  locals &other\nend",
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			if _, errs := parseSource(t, source); len(errs) != 0 {
				t.Fatalf("parseSource(%q) errors: %v", source, errs)
			}
		})
	}
}
