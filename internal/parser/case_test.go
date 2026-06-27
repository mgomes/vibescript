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
						Values: []ast.CaseWhenValue{
							{Expr: &ast.BinaryExpr{
								Left:     &ast.Identifier{Name: "value"},
								Operator: ast.TokenEQ,
								Right:    &ast.IntegerLiteral{Value: 1},
							}},
						},
						Result: &ast.StringLiteral{Value: "one"},
					},
					{
						Values: []ast.CaseWhenValue{
							{Expr: &ast.BinaryExpr{
								Left:     &ast.Identifier{Name: "value"},
								Operator: ast.TokenEQ,
								Right:    &ast.IntegerLiteral{Value: 2},
							}},
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

func TestParserCaseWhenThenSeparators(t *testing.T) {
	t.Parallel()

	source := `def run
  case 2
  when 1, 3 then "odd"
  when 2 then "two"
  else "other"
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.CaseExpr{
				Target: &ast.IntegerLiteral{Value: 2},
				Clauses: []ast.CaseWhenClause{
					{
						Values: []ast.CaseWhenValue{
							{Expr: &ast.IntegerLiteral{Value: 1}},
							{Expr: &ast.IntegerLiteral{Value: 3}},
						},
						Result: &ast.StringLiteral{Value: "odd"},
					},
					{
						Values: []ast.CaseWhenValue{
							{Expr: &ast.IntegerLiteral{Value: 2}},
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

func TestParserCaseWhenSplatValues(t *testing.T) {
	t.Parallel()

	source := `def run
  case value
  when 1, *choices then "matched"
  else "other"
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.CaseExpr{
				Target: &ast.Identifier{Name: "value"},
				Clauses: []ast.CaseWhenClause{
					{
						Values: []ast.CaseWhenValue{
							{Expr: &ast.IntegerLiteral{Value: 1}},
							{Expr: &ast.Identifier{Name: "choices"}, Splat: true},
						},
						Result: &ast.StringLiteral{Value: "matched"},
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
