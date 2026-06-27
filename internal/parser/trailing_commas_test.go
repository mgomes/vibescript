package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserTrailingCommasInLiteralsAndCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{
			name: "single_line",
			source: `def run
  add([1, 2,], {a: 1, b: 2,}, c: 3,)
end`,
		},
		{
			name: "multi_line",
			source: `def run
  add(
    [
      1,
      2,
    ],
    {
      a: 1,
      b: 2,
    },
    c: 3,
  )
end`,
		},
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.Identifier{Name: "add"},
			Args: []ast.Expression{
				&ast.ArrayLiteral{
					Elements: []ast.Expression{
						&ast.IntegerLiteral{Value: 1},
						&ast.IntegerLiteral{Value: 2},
					},
				},
				&ast.HashLiteral{
					Pairs: []ast.HashPair{
						{
							Key:   &ast.SymbolLiteral{Name: "a"},
							Value: &ast.IntegerLiteral{Value: 1},
						},
						{
							Key:   &ast.SymbolLiteral{Name: "b"},
							Value: &ast.IntegerLiteral{Value: 2},
						},
					},
				},
			},
			KwArgs: []ast.KeywordArg{
				{Name: "c", Value: &ast.IntegerLiteral{Value: 3}},
			},
			KeywordOptionsHash: true,
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}

			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestParserTrailingHashCommaWithValueOmission verifies that a trailing comma
// after an omitted label value still expands to the local-variable shorthand,
// so {a:,} is parsed as {a: a} just like {a:}.
func TestParserTrailingHashCommaWithValueOmission(t *testing.T) {
	t.Parallel()

	source := `def run
  {a:,}
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
						Key:   &ast.SymbolLiteral{Name: "a"},
						Value: &ast.Identifier{Name: "a"},
					},
				},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
