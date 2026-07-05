package parser

import (
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

func TestParserBeginlessRange(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		expr      string
		exclusive bool
	}{{"..5", false}, {"...5", true}} {
		tc := tc
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			source := `def run
  ` + tc.expr + `
end`

			got, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
			}
			wantBody := []ast.Statement{
				&ast.ExprStmt{Expr: &ast.RangeExpr{
					End:       &ast.IntegerLiteral{Value: 5},
					Exclusive: tc.exclusive,
				}},
			}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParserEndlessRange(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		expr      string
		exclusive bool
	}{{"1..", false}, {"1...", true}} {
		tc := tc
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			source := `def run
  ` + tc.expr + `
end`

			got, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
			}
			wantBody := []ast.Statement{
				&ast.ExprStmt{Expr: &ast.RangeExpr{
					Start:     &ast.IntegerLiteral{Value: 1},
					Exclusive: tc.exclusive,
				}},
			}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestParserBareMultilineRangeIsEndless pins the Ruby rule that a newline
// terminates a range at statement level: bare dots at end of line form an
// endless range and the next line is a separate statement. Grouped forms
// (parens, brackets, call arguments) still continue onto the next line — see
// TestParserAllowsMultilineRangeEndpointInCallArgument.
func TestParserBareMultilineRangeIsEndless(t *testing.T) {
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
		}},
		&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 2}},
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
