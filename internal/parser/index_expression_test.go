package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

// TestParserIndexExpressionForms covers the selector shapes a bracket access can
// carry: a single index, a start/length pair, and a range. Each parses into an
// IndexExpr whose Indices slice holds the comma-separated selectors.
func TestParserIndexExpressionForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   *ast.IndexExpr
	}{
		{
			name:   "single index",
			source: "def run() a[0] end",
			want: &ast.IndexExpr{
				Object:  &ast.Identifier{Name: "a"},
				Indices: []ast.Expression{&ast.IntegerLiteral{Value: 0}},
			},
		},
		{
			name:   "negative index",
			source: "def run() a[-1] end",
			want: &ast.IndexExpr{
				Object: &ast.Identifier{Name: "a"},
				Indices: []ast.Expression{
					&ast.UnaryExpr{Operator: ast.TokenMinus, Right: &ast.IntegerLiteral{Value: 1}},
				},
			},
		},
		{
			name:   "start and length",
			source: "def run() a[1, 2] end",
			want: &ast.IndexExpr{
				Object: &ast.Identifier{Name: "a"},
				Indices: []ast.Expression{
					&ast.IntegerLiteral{Value: 1},
					&ast.IntegerLiteral{Value: 2},
				},
			},
		},
		{
			name:   "range",
			source: "def run() a[1..2] end",
			want: &ast.IndexExpr{
				Object: &ast.Identifier{Name: "a"},
				Indices: []ast.Expression{
					&ast.RangeExpr{
						Start: &ast.IntegerLiteral{Value: 1},
						End:   &ast.IntegerLiteral{Value: 2},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			program, errs := parseSource(t, tt.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tt.source, errs)
			}
			body := parsedFunctionBody(t, program)
			if len(body) != 1 {
				t.Fatalf("function body has %d statements, want 1", len(body))
			}
			stmt, ok := body[0].(*ast.ExprStmt)
			if !ok {
				t.Fatalf("statement = %T, want *ast.ExprStmt", body[0])
			}
			if diff := cmp.Diff(tt.want, stmt.Expr, astCmpOpts); diff != "" {
				t.Fatalf("index expression mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestParserEmptyIndexExpressionIsError verifies that a[] is rejected: a bracket
// access requires at least one selector.
func TestParserEmptyIndexExpressionIsError(t *testing.T) {
	t.Parallel()
	_, errs := parseSource(t, "def run() a[] end")
	if len(errs) == 0 {
		t.Fatal("parseSource(empty index) errors = nil, want a missing-selector diagnostic")
	}
}
