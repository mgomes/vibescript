package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserFiniteRangeExpression(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want *ast.RangeExpr
	}{
		{
			name: "inclusive",
			expr: "1..5",
			want: &ast.RangeExpr{
				Start: &ast.IntegerLiteral{Value: 1},
				End:   &ast.IntegerLiteral{Value: 5},
			},
		},
		{
			name: "exclusive",
			expr: "1...5",
			want: &ast.RangeExpr{
				Start:     &ast.IntegerLiteral{Value: 1},
				End:       &ast.IntegerLiteral{Value: 5},
				Exclusive: true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source := `def run
  ` + tc.expr + `
end`
			got, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
			}

			wantBody := []ast.Statement{
				&ast.ExprStmt{Expr: tc.want},
			}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParserRejectsBeginlessRange(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"..5", "...5"} {
		expr := expr
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			source := `def run
  ` + expr + `
end`

			_, errs := parseSource(t, source)
			if len(errs) != 1 {
				t.Fatalf("parseSource(%q) errors = %d, want 1: %v", source, len(errs), errs)
			}
			if got, want := errs[0].Error(), "range is missing start expression"; !strings.Contains(got, want) {
				t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
			}
		})
	}
}

func TestParserRejectsEndlessRange(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"1..", "1..."} {
		expr := expr
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			source := `def run
  ` + expr + `
end`

			_, errs := parseSource(t, source)
			if len(errs) != 1 {
				t.Fatalf("parseSource(%q) errors = %d, want 1: %v", source, len(errs), errs)
			}
			if got, want := errs[0].Error(), "range is missing end expression"; !strings.Contains(got, want) {
				t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
			}
		})
	}
}

func TestParserAllowsMultilineRangeEndpoint(t *testing.T) {
	t.Parallel()

	source := `def run
  1..
    2
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.RangeExpr{
			Start: &ast.IntegerLiteral{Value: 1},
			End:   &ast.IntegerLiteral{Value: 2},
		}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserAllowsMultilineRangeEndpointInCallArgument(t *testing.T) {
	t.Parallel()

	source := `def run
  foo(1..
    2)
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.Identifier{Name: "foo"},
			Args: []ast.Expression{
				&ast.RangeExpr{
					Start: &ast.IntegerLiteral{Value: 1},
					End:   &ast.IntegerLiteral{Value: 2},
				},
			},
			KwArgs: []ast.KeywordArg{},
		}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
