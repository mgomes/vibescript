package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserBlockParameterDestructuring(t *testing.T) {
	t.Parallel()

	source := `def run
  pairs.map do |(a, b), label|
    a + b
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	stmt, ok := body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("body[0] = %T, want *ast.ExprStmt", body[0])
	}
	call, ok := stmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("body[0].Expr = %T, want *ast.CallExpr", stmt.Expr)
	}
	if call.Block == nil {
		t.Fatal("call block = nil, want block")
	}

	want := []ast.Param{
		{
			Target: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "a"}},
				{Target: &ast.Identifier{Name: "b"}},
			}},
		},
		{Name: "label"},
	}
	if diff := cmp.Diff(want, call.Block.Params, astCmpOpts); diff != "" {
		t.Fatalf("block params mismatch (-want +got):\n%s", diff)
	}
}

func TestParserBlockParameterDestructuringRejectsNonIdentifierTargets(t *testing.T) {
	t.Parallel()

	source := `def run
  values.map do |(record.value)|
    record
  end
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = nil, want invalid destructuring target error", source)
	}
	if got, want := errs[0].Error(), "invalid block parameter destructuring target"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}
