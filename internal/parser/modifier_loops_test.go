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

func TestParserModifierIfConditional(t *testing.T) {
	t.Parallel()

	source := `def run(flag)
  value = "ok" if flag
  "done" if true
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.IfStmt{
			Condition: &ast.Identifier{Name: "flag"},
			Consequent: []ast.Statement{
				&ast.AssignStmt{
					Target: &ast.Identifier{Name: "value"},
					Value:  &ast.StringLiteral{Value: "ok"},
				},
			},
		},
		&ast.IfStmt{
			Condition: &ast.BoolLiteral{Value: true},
			Consequent: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.StringLiteral{Value: "done"}},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserModifierUnlessConditional(t *testing.T) {
	t.Parallel()

	source := `def run(flag)
  value = "ok" unless flag
  "done" unless false
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.IfStmt{
			Condition: &ast.Identifier{Name: "flag"},
			Alternate: []ast.Statement{
				&ast.AssignStmt{
					Target: &ast.Identifier{Name: "value"},
					Value:  &ast.StringLiteral{Value: "ok"},
				},
			},
		},
		&ast.IfStmt{
			Condition: &ast.BoolLiteral{Value: false},
			Alternate: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.StringLiteral{Value: "done"}},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserModifierIfRejectsComplexStatements(t *testing.T) {
	t.Parallel()

	source := `def run
  while ready
    tick
  end if again
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = nil, want modifier if placement error", source)
	}
	if got, want := errs[0].Error(), "modifier if is only supported after expression or assignment statements"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
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

func TestParserModifierUnlessRejectsComplexStatements(t *testing.T) {
	t.Parallel()

	source := `def run
  while ready
    tick
  end unless done
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = nil, want modifier unless placement error", source)
	}
	if got, want := errs[0].Error(), "modifier unless is only supported after expression or assignment statements"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}
