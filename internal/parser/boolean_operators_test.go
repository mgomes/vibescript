package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserWordBooleanOperators(t *testing.T) {
	t.Parallel()

	source := `def run
  not false and true or false
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.BinaryExpr{
				Left: &ast.BinaryExpr{
					Left: &ast.UnaryExpr{
						Operator: ast.TokenNot,
						Right:    &ast.BoolLiteral{Value: false},
					},
					Operator: ast.TokenAnd,
					Right:    &ast.BoolLiteral{Value: true},
				},
				Operator: ast.TokenOr,
				Right:    &ast.BoolLiteral{Value: false},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserRejectsSymbolicBooleanLabels(t *testing.T) {
	t.Parallel()

	cases := []string{
		`def run
  {&&: 1}
end`,
		`def run
  takes(||: 2)
end`,
	}
	for _, source := range cases {
		_, errs := parseSource(t, source)
		if len(errs) == 0 {
			t.Fatalf("parseSource(%q) errors = none, want symbolic boolean label error", source)
		}
	}
}
