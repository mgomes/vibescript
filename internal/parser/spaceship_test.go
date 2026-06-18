package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserSpaceshipExpression(t *testing.T) {
	t.Parallel()

	source := `def run
  left <=> right
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "left"},
				Operator: ast.TokenSpaceship,
				Right:    &ast.Identifier{Name: "right"},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserSpaceshipMemberCall(t *testing.T) {
	t.Parallel()

	source := `def run
  left.<=>(right)
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.CallExpr{
				Callee: &ast.MemberExpr{
					Object:   &ast.Identifier{Name: "left"},
					Property: "<=>",
				},
				Args:   []ast.Expression{&ast.Identifier{Name: "right"}},
				KwArgs: []ast.KeywordArg{},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserSpaceshipContinuesAcrossNewline(t *testing.T) {
	t.Parallel()

	source := `def run
  result = left
    <=>
    right
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "result"},
			Value: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "left"},
				Operator: ast.TokenSpaceship,
				Right:    &ast.Identifier{Name: "right"},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
