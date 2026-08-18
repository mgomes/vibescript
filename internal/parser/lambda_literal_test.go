package parser

import (
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

// The stabby lambda was the shortest way to turn executable code into a value.
// ADR-006 removed it, and the ADR's own example is the case an author is most
// likely to write, so it is pinned here with the diagnostic it must produce.
func TestParserStabbyLambdaIsRemoved(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		"def run\n  mapper = ->(person) { person.name }\nend",
		"def run\n  fn = ->(a, b) { a + b }\nend",
		"def run\n  fn = -> { 1 }\nend",
		"def run\n  fn = ->(n) do\n    n\n  end\nend",
		"def run\n  ->(n) { n }\nend",
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, source)
			if len(errs) == 0 {
				t.Fatal("expected a parse error for a lambda literal")
			}
			joined := joinTeachingErrors(errs)
			if !strings.Contains(joined, "lambda literals are not supported") {
				t.Fatalf("error = %s, want it to name the removal", joined)
			}
			if !strings.Contains(joined, "named function") || !strings.Contains(joined, "block") {
				t.Fatalf("error = %s, want it to name both replacements", joined)
			}
		})
	}
}

// `->` remains the return-type annotation on a def signature line. Removing
// the lambda literal must not take the annotation with it.
func TestParserReturnAnnotationStillParses(t *testing.T) {
	t.Parallel()

	program, errs := parseSource(t, `def annotated(x) -> Int
  x
end`)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	annotated, ok := program.Statements[0].(*ast.FunctionStmt)
	if !ok || annotated.ReturnTy == nil {
		t.Fatalf("annotated = %T, want a function with a return annotation", program.Statements[0])
	}
}
