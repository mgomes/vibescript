package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserKeywordArgumentShorthand(t *testing.T) {
	t.Parallel()

	source := `def run
  takes(name:, age: 42)
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.Identifier{Name: "takes"},
			Args:   []ast.Expression{},
			KwArgs: []ast.KeywordArg{
				{Name: "name", Value: &ast.Identifier{Name: "name"}},
				{Name: "age", Value: &ast.IntegerLiteral{Value: 42}},
			},
		}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
