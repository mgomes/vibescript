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

func TestParserHashRocketKeys(t *testing.T) {
	t.Parallel()

	source := `def run
  {:name => "Ada", "first-name" => "Lovelace", key => 1}
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
						Key:   &ast.SymbolLiteral{Name: "name"},
						Value: &ast.StringLiteral{Value: "Ada"},
					},
					{
						Key:   &ast.StringLiteral{Value: "first-name"},
						Value: &ast.StringLiteral{Value: "Lovelace"},
					},
					{
						Key:   &ast.Identifier{Name: "key"},
						Value: &ast.IntegerLiteral{Value: 1},
					},
				},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserWordBooleanHashKeys(t *testing.T) {
	t.Parallel()

	source := `def run
  {and: 1, or: 2, not: 3}
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
						Key:   &ast.SymbolLiteral{Name: "and"},
						Value: &ast.IntegerLiteral{Value: 1},
					},
					{
						Key:   &ast.SymbolLiteral{Name: "or"},
						Value: &ast.IntegerLiteral{Value: 2},
					},
					{
						Key:   &ast.SymbolLiteral{Name: "not"},
						Value: &ast.IntegerLiteral{Value: 3},
					},
				},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
