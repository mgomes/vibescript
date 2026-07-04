package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserRescueModifierExpression(t *testing.T) {
	t.Parallel()

	source := `def run
  x = risky rescue fallback
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "x"},
			Value: &ast.RescueExpr{
				Body:     &ast.Identifier{Name: "risky"},
				Fallback: &ast.Identifier{Name: "fallback"},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserRescueModifierPrecedence(t *testing.T) {
	t.Parallel()

	source := `def run
  x = 1 + risky rescue fallback + 2
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "x"},
			Value: &ast.RescueExpr{
				Body: &ast.BinaryExpr{
					Left:     &ast.IntegerLiteral{Value: 1},
					Operator: ast.TokenPlus,
					Right:    &ast.Identifier{Name: "risky"},
				},
				Fallback: &ast.BinaryExpr{
					Left:     &ast.Identifier{Name: "fallback"},
					Operator: ast.TokenPlus,
					Right:    &ast.IntegerLiteral{Value: 2},
				},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserRescueModifierKeepsKeywordLabelCall(t *testing.T) {
	t.Parallel()

	source := `def run
  record rescue: "retry"
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "record"},
				Args:   []ast.Expression{},
				KwArgs: []ast.KeywordArg{
					{Name: "rescue", Value: &ast.StringLiteral{Value: "retry"}},
				},
				KeywordOptionsHash: true,
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserRescueModifierRequiresFallbackExpression(t *testing.T) {
	t.Parallel()

	source := `def run
  risky rescue
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = none, want rescue fallback diagnostic", source)
	}
	if got, want := errs[0].Error(), "rescue modifier requires fallback expression"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}
