package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserExponentOperatorPrecedenceAndAssociativity(t *testing.T) {
	t.Parallel()

	source := `def run
  2 ** 3 ** 2
  -2 ** 2
  2 ** -3
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.BinaryExpr{
				Left:     &ast.IntegerLiteral{Value: 2},
				Operator: ast.TokenPower,
				Right: &ast.BinaryExpr{
					Left:     &ast.IntegerLiteral{Value: 3},
					Operator: ast.TokenPower,
					Right:    &ast.IntegerLiteral{Value: 2},
				},
			},
		},
		&ast.ExprStmt{
			Expr: &ast.UnaryExpr{
				Operator: ast.TokenMinus,
				Right: &ast.BinaryExpr{
					Left:     &ast.IntegerLiteral{Value: 2},
					Operator: ast.TokenPower,
					Right:    &ast.IntegerLiteral{Value: 2},
				},
			},
		},
		&ast.ExprStmt{
			Expr: &ast.BinaryExpr{
				Left:     &ast.IntegerLiteral{Value: 2},
				Operator: ast.TokenPower,
				Right: &ast.UnaryExpr{
					Operator: ast.TokenMinus,
					Right:    &ast.IntegerLiteral{Value: 3},
				},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserExponentContinuesAcrossNewline(t *testing.T) {
	t.Parallel()

	source := `def run
  value = 2
    ** 3
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "value"},
			Value: &ast.BinaryExpr{
				Left:     &ast.IntegerLiteral{Value: 2},
				Operator: ast.TokenPower,
				Right:    &ast.IntegerLiteral{Value: 3},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
