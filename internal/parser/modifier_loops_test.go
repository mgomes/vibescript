package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserModifierWhileLoop(t *testing.T) {
	t.Parallel()

	source := `def run
  i = 0
  i = i + 1 while i < 3
  i
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{Target: &ast.Identifier{Name: "i"}, Value: &ast.IntegerLiteral{Value: 0}},
		&ast.WhileStmt{
			Condition: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "i"},
				Operator: ast.TokenLT,
				Right:    &ast.IntegerLiteral{Value: 3},
			},
			Body: []ast.Statement{
				&ast.AssignStmt{
					Target: &ast.Identifier{Name: "i"},
					Value: &ast.BinaryExpr{
						Left:     &ast.Identifier{Name: "i"},
						Operator: ast.TokenPlus,
						Right:    &ast.IntegerLiteral{Value: 1},
					},
				},
			},
		},
		&ast.ExprStmt{Expr: &ast.Identifier{Name: "i"}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserModifierUntilLoop(t *testing.T) {
	t.Parallel()

	source := `def run
  i = 0
  i = i + 1 until i >= 3
  i
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	if len(body) != 3 {
		t.Fatalf("function body length = %d, want 3", len(body))
	}
	if _, ok := body[1].(*ast.UntilStmt); !ok {
		t.Fatalf("body[1] = %T, want *ast.UntilStmt", body[1])
	}
}

func TestParserModifierLoopRejectsComplexStatements(t *testing.T) {
	t.Parallel()

	source := `def run
  while ready
    tick
  end while again
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = nil, want modifier loop placement error", source)
	}
	if got, want := errs[0].Error(), "modifier while is only supported after expression or assignment statements"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}
