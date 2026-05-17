package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserYieldWithoutParensDoesNotConsumeNextLineInAssignment(t *testing.T) {
	t.Parallel()
	source := `def run
  result = yield
  elapsed = 1
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	want := &ast.Program{
		Statements: []ast.Statement{
			&ast.FunctionStmt{
				Name:   "run",
				Params: []ast.Param{},
				Body: []ast.Statement{
					&ast.AssignStmt{
						Target: &ast.Identifier{Name: "result"},
						Value:  &ast.YieldExpr{},
					},
					&ast.AssignStmt{
						Target: &ast.Identifier{Name: "elapsed"},
						Value:  &ast.IntegerLiteral{Value: 1},
					},
				},
			},
		},
	}

	if diff := cmp.Diff(want, got, astCmpOpts); diff != "" {
		t.Fatalf("program mismatch (-want +got):\n%s", diff)
	}
}

func TestParserYieldWithoutParensAcceptsInlineArgument(t *testing.T) {
	t.Parallel()
	source := `def run
  result = yield value
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	want := &ast.Program{
		Statements: []ast.Statement{
			&ast.FunctionStmt{
				Name:   "run",
				Params: []ast.Param{},
				Body: []ast.Statement{
					&ast.AssignStmt{
						Target: &ast.Identifier{Name: "result"},
						Value: &ast.YieldExpr{
							Args: []ast.Expression{&ast.Identifier{Name: "value"}},
						},
					},
				},
			},
		},
	}

	if diff := cmp.Diff(want, got, astCmpOpts); diff != "" {
		t.Fatalf("program mismatch (-want +got):\n%s", diff)
	}
}
