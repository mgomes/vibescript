package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserBeginRescueElseEnsure(t *testing.T) {
	t.Parallel()

	source := `def run
  begin
    1
  rescue
    2
  else
    3
  ensure
    4
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.TryStmt{
			Body: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 1}},
			},
			Rescue: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 2}},
			},
			Else: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 3}},
			},
			Ensure: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 4}},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserBeginElseWithoutRescue(t *testing.T) {
	t.Parallel()

	source := `def run
  begin
    1
  else
    2
  end
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = nil, want begin else diagnostic", source)
	}
	if got, want := errs[0].Error(), "begin else requires rescue"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}
