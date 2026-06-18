package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserUnlessStatement(t *testing.T) {
	t.Parallel()

	source := `def run(flag)
  unless flag
    "ok"
  else
    "blocked"
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.IfStmt{
			Condition: &ast.Identifier{Name: "flag"},
			Consequent: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.StringLiteral{Value: "blocked"}},
			},
			Alternate: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.StringLiteral{Value: "ok"}},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserUnlessWithoutElse(t *testing.T) {
	t.Parallel()

	source := `def run(flag)
  unless flag
    "ok"
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.IfStmt{
			Condition: &ast.Identifier{Name: "flag"},
			Alternate: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.StringLiteral{Value: "ok"}},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
