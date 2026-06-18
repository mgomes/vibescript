package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserTargetlessCaseExpression(t *testing.T) {
	t.Parallel()

	source := `def run
  case
  when value == 1
    "one"
  when value == 2
    "two"
  else
    "other"
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.CaseExpr{
				Clauses: []ast.CaseWhenClause{
					{
						Values: []ast.Expression{
							&ast.BinaryExpr{
								Left:     &ast.Identifier{Name: "value"},
								Operator: ast.TokenEQ,
								Right:    &ast.IntegerLiteral{Value: 1},
							},
						},
						Result: &ast.StringLiteral{Value: "one"},
					},
					{
						Values: []ast.Expression{
							&ast.BinaryExpr{
								Left:     &ast.Identifier{Name: "value"},
								Operator: ast.TokenEQ,
								Right:    &ast.IntegerLiteral{Value: 2},
							},
						},
						Result: &ast.StringLiteral{Value: "two"},
					},
				},
				ElseExpr: &ast.StringLiteral{Value: "other"},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
