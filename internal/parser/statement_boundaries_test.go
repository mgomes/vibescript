package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserLineInitialBracketStartsNewStatement(t *testing.T) {
	t.Parallel()
	source := `def run
  x = [1, 2, 3].first(2)
  [4, 5].first(1)
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "x"},
			Value: &ast.CallExpr{
				Callee: &ast.MemberExpr{
					Object: &ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.IntegerLiteral{Value: 1},
						&ast.IntegerLiteral{Value: 2},
						&ast.IntegerLiteral{Value: 3},
					}},
					Property: "first",
				},
				Args:   []ast.Expression{&ast.IntegerLiteral{Value: 2}},
				KwArgs: []ast.KeywordArg{},
			},
		},
		&ast.ExprStmt{
			Expr: &ast.CallExpr{
				Callee: &ast.MemberExpr{
					Object: &ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.IntegerLiteral{Value: 4},
						&ast.IntegerLiteral{Value: 5},
					}},
					Property: "first",
				},
				Args:   []ast.Expression{&ast.IntegerLiteral{Value: 1}},
				KwArgs: []ast.KeywordArg{},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserLineInitialParenStartsNewStatement(t *testing.T) {
	t.Parallel()
	source := `def run
  x = value
  (1 + 2)
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "x"},
			Value:  &ast.Identifier{Name: "value"},
		},
		&ast.ExprStmt{
			Expr: &ast.BinaryExpr{
				Left:     &ast.IntegerLiteral{Value: 1},
				Operator: ast.TokenPlus,
				Right:    &ast.IntegerLiteral{Value: 2},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserBareAssertKeepsNextLineStatementsSeparate(t *testing.T) {
	t.Parallel()
	source := `def run
  assert
  [1]
  (2)
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.Identifier{Name: "assert"}},
		&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
			&ast.IntegerLiteral{Value: 1},
		}}},
		&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 2}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserExplicitContinuationAcrossNewline(t *testing.T) {
	t.Parallel()
	source := `def run
  x = [1, 2, 3]
    .first(2)
  y = 1
    + 2
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "x"},
			Value: &ast.CallExpr{
				Callee: &ast.MemberExpr{
					Object: &ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.IntegerLiteral{Value: 1},
						&ast.IntegerLiteral{Value: 2},
						&ast.IntegerLiteral{Value: 3},
					}},
					Property: "first",
				},
				Args:   []ast.Expression{&ast.IntegerLiteral{Value: 2}},
				KwArgs: []ast.KeywordArg{},
			},
		},
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "y"},
			Value: &ast.BinaryExpr{
				Left:     &ast.IntegerLiteral{Value: 1},
				Operator: ast.TokenPlus,
				Right:    &ast.IntegerLiteral{Value: 2},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserAssignmentEqualsContinuesAcrossNewline(t *testing.T) {
	t.Parallel()
	source := `def run
  x
    = 1
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "x"},
			Value:  &ast.IntegerLiteral{Value: 1},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserMinusWithWhitespaceContinuesAcrossNewline(t *testing.T) {
	t.Parallel()
	source := `def run
  x = 10
    - 3
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "x"},
			Value: &ast.BinaryExpr{
				Left:     &ast.IntegerLiteral{Value: 10},
				Operator: ast.TokenMinus,
				Right:    &ast.IntegerLiteral{Value: 3},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserLineInitialMinusStartsBlockStatement(t *testing.T) {
	t.Parallel()
	source := `def run(flag)
  if flag
    -1
  else
    1
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	wantBody := []ast.Statement{
		&ast.IfStmt{
			Condition: &ast.Identifier{Name: "flag"},
			Consequent: []ast.Statement{
				&ast.ExprStmt{
					Expr: &ast.UnaryExpr{
						Operator: ast.TokenMinus,
						Right:    &ast.IntegerLiteral{Value: 1},
					},
				},
			},
			Alternate: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 1}},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func parsedFunctionBody(t testing.TB, program *ast.Program) []ast.Statement {
	t.Helper()
	if len(program.Statements) != 1 {
		t.Fatalf("parseSource(function) returned %d statements, want 1", len(program.Statements))
	}
	fn, ok := program.Statements[0].(*ast.FunctionStmt)
	if !ok {
		t.Fatalf("parseSource(function) statement = %T, want *ast.FunctionStmt", program.Statements[0])
	}
	return fn.Body
}
