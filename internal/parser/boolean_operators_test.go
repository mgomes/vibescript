package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserSymbolicBooleanOperators(t *testing.T) {
	t.Parallel()

	source := `def run
  !false && true || false
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
						Operator: ast.TokenBang,
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

func TestParserWordBooleanNamesAreIdentifiers(t *testing.T) {
	t.Parallel()

	source := `def run
  [and, or, not]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
				&ast.Identifier{Name: "and"},
				&ast.Identifier{Name: "or"},
				&ast.Identifier{Name: "not"},
			}},
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
