package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserQuotedHashKeys(t *testing.T) {
	t.Parallel()

	source := `def run
  {"name": "Ada", "first-name": "Lovelace", active: true}
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.HashLiteral{
				Pairs: []ast.HashPair{
					{
						Key:   &ast.StringLiteral{Value: "name"},
						Value: &ast.StringLiteral{Value: "Ada"},
					},
					{
						Key:   &ast.StringLiteral{Value: "first-name"},
						Value: &ast.StringLiteral{Value: "Lovelace"},
					},
					{
						Key:   &ast.SymbolLiteral{Name: "active"},
						Value: &ast.BoolLiteral{Value: true},
					},
				},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
