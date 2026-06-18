package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserIfExpressions(t *testing.T) {
	t.Parallel()

	source := `def run
  label = if value == 1
    "one"
  elsif value == 2
    "two"
  else
    "other"
  end

  missing = if enabled
    "enabled"
  end

  return if flag
    "yes"
  else
    "no"
  end

  pick(if flag then "yes" else "no" end)
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "label"},
			Value: &ast.IfExpr{
				Condition: &ast.BinaryExpr{
					Left:     &ast.Identifier{Name: "value"},
					Operator: ast.TokenEQ,
					Right:    &ast.IntegerLiteral{Value: 1},
				},
				Consequent: &ast.StringLiteral{Value: "one"},
				ElseIf: []ast.IfExprBranch{
					{
						Condition: &ast.BinaryExpr{
							Left:     &ast.Identifier{Name: "value"},
							Operator: ast.TokenEQ,
							Right:    &ast.IntegerLiteral{Value: 2},
						},
						Result: &ast.StringLiteral{Value: "two"},
					},
				},
				Alternate: &ast.StringLiteral{Value: "other"},
			},
		},
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "missing"},
			Value: &ast.IfExpr{
				Condition:  &ast.Identifier{Name: "enabled"},
				Consequent: &ast.StringLiteral{Value: "enabled"},
			},
		},
		&ast.ReturnStmt{
			Value: &ast.IfExpr{
				Condition:  &ast.Identifier{Name: "flag"},
				Consequent: &ast.StringLiteral{Value: "yes"},
				Alternate:  &ast.StringLiteral{Value: "no"},
			},
		},
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.Identifier{Name: "pick"},
			Args: []ast.Expression{
				&ast.IfExpr{
					Condition:  &ast.Identifier{Name: "flag"},
					Consequent: &ast.StringLiteral{Value: "yes"},
					Alternate:  &ast.StringLiteral{Value: "no"},
				},
			},
			KwArgs: []ast.KeywordArg{},
		}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
