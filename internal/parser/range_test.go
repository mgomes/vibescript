package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserFiniteRangeExpression(t *testing.T) {
	t.Parallel()

	source := `def run
  1..5
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.RangeExpr{
				Start: &ast.IntegerLiteral{Value: 1},
				End:   &ast.IntegerLiteral{Value: 5},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserRejectsBeginlessRange(t *testing.T) {
	t.Parallel()

	source := `def run
  ..5
end`

	_, errs := parseSource(t, source)
	if len(errs) != 1 {
		t.Fatalf("parseSource(%q) errors = %d, want 1: %v", source, len(errs), errs)
	}
	if got, want := errs[0].Error(), "range is missing start expression"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}

func TestParserRejectsEndlessRange(t *testing.T) {
	t.Parallel()

	source := `def run
  1..
end`

	_, errs := parseSource(t, source)
	if len(errs) != 1 {
		t.Fatalf("parseSource(%q) errors = %d, want 1: %v", source, len(errs), errs)
	}
	if got, want := errs[0].Error(), "range is missing end expression"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}
