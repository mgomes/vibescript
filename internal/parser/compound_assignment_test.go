package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserCompoundAssignment(t *testing.T) {
	t.Parallel()

	source := `def run
  total = 1
  total += 2
  items[0] *= 3
  power **= 2
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "total"},
			Value:  &ast.IntegerLiteral{Value: 1},
		},
		&ast.AssignStmt{
			Target:   &ast.Identifier{Name: "total"},
			Value:    &ast.IntegerLiteral{Value: 2},
			Operator: ast.TokenPlus,
		},
		&ast.AssignStmt{
			Target: &ast.IndexExpr{
				Object: &ast.Identifier{Name: "items"},
				Index:  &ast.IntegerLiteral{Value: 0},
			},
			Value:    &ast.IntegerLiteral{Value: 3},
			Operator: ast.TokenAsterisk,
		},
		&ast.AssignStmt{
			Target:   &ast.Identifier{Name: "power"},
			Value:    &ast.IntegerLiteral{Value: 2},
			Operator: ast.TokenPower,
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserRejectsCompoundDestructuringAssignment(t *testing.T) {
	t.Parallel()

	source := `def run
  a, b += [1, 2]
end`

	_, errs := parseSource(t, source)
	if len(errs) != 1 {
		t.Fatalf("parseSource(%q) errors = %d, want 1: %v", source, len(errs), errs)
	}
	if got, want := errs[0].Error(), "compound assignment is not supported for destructuring targets"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}
