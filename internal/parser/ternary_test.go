package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserTernaryConditionalExpression(t *testing.T) {
	t.Parallel()

	source := `def run
  active ? user.name("short") : "fallback"
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.ConditionalExpr{
				Condition: &ast.Identifier{Name: "active"},
				Consequent: &ast.CallExpr{
					Callee: &ast.MemberExpr{
						Object:   &ast.Identifier{Name: "user"},
						Property: "name",
					},
					Args:   []ast.Expression{&ast.StringLiteral{Value: "short"}},
					KwArgs: []ast.KeywordArg{},
				},
				Alternate: &ast.StringLiteral{Value: "fallback"},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserTernaryPrecedence(t *testing.T) {
	t.Parallel()

	source := `def run
  true || false ? 1 : 2 + 3
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.ConditionalExpr{
				Condition: &ast.BinaryExpr{
					Left:     &ast.BoolLiteral{Value: true},
					Operator: ast.TokenOr,
					Right:    &ast.BoolLiteral{Value: false},
				},
				Consequent: &ast.IntegerLiteral{Value: 1},
				Alternate: &ast.BinaryExpr{
					Left:     &ast.IntegerLiteral{Value: 2},
					Operator: ast.TokenPlus,
					Right:    &ast.IntegerLiteral{Value: 3},
				},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserTernaryIsRightAssociative(t *testing.T) {
	t.Parallel()

	source := `def run
  first ? 1 : second ? 2 : 3
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.ConditionalExpr{
				Condition:  &ast.Identifier{Name: "first"},
				Consequent: &ast.IntegerLiteral{Value: 1},
				Alternate: &ast.ConditionalExpr{
					Condition:  &ast.Identifier{Name: "second"},
					Consequent: &ast.IntegerLiteral{Value: 2},
					Alternate:  &ast.IntegerLiteral{Value: 3},
				},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserTernaryAllowsMultilineBranches(t *testing.T) {
	t.Parallel()

	source := `def run
  flag ?
    choose(1)
  : choose(2)
end`

	if _, errs := parseSource(t, source); len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
}
