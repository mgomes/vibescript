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

func TestParserYieldWithoutParensAcceptsMultipleInlineArguments(t *testing.T) {
	t.Parallel()
	source := `def run
  result = yield first, second + 1
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
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
							Args: []ast.Expression{
								&ast.Identifier{Name: "first"},
								&ast.BinaryExpr{
									Left:     &ast.Identifier{Name: "second"},
									Operator: ast.TokenPlus,
									Right:    &ast.IntegerLiteral{Value: 1},
								},
							},
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

func TestParserYieldWithoutParensStopsInlineArgumentsAtNewline(t *testing.T) {
	t.Parallel()
	source := `def run
  yield value
  done
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.YieldExpr{
			Args: []ast.Expression{&ast.Identifier{Name: "value"}},
		}},
		&ast.ExprStmt{Expr: &ast.Identifier{Name: "done"}},
	}

	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserYieldWithParensAcceptsTrailingComma(t *testing.T) {
	t.Parallel()

	source := `def run
  yield(first, second,)
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.YieldExpr{
			Args: []ast.Expression{
				&ast.Identifier{Name: "first"},
				&ast.Identifier{Name: "second"},
			},
		}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
