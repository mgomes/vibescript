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

func TestParserParenlessSingleArgumentCalls(t *testing.T) {
	t.Parallel()
	source := `def run
  id 1
  [1].push 2
  x = id 3
  return id 4
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "id"},
				Args:   []ast.Expression{&ast.IntegerLiteral{Value: 1}},
				KwArgs: []ast.KeywordArg{},
			},
		},
		&ast.ExprStmt{
			Expr: &ast.CallExpr{
				Callee: &ast.MemberExpr{
					Object: &ast.ArrayLiteral{Elements: []ast.Expression{
						&ast.IntegerLiteral{Value: 1},
					}},
					Property: "push",
				},
				Args:   []ast.Expression{&ast.IntegerLiteral{Value: 2}},
				KwArgs: []ast.KeywordArg{},
			},
		},
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "x"},
			Value: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "id"},
				Args:   []ast.Expression{&ast.IntegerLiteral{Value: 3}},
				KwArgs: []ast.KeywordArg{},
			},
		},
		&ast.ReturnStmt{
			Value: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "id"},
				Args:   []ast.Expression{&ast.IntegerLiteral{Value: 4}},
				KwArgs: []ast.KeywordArg{},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserParenlessArgumentListCalls(t *testing.T) {
	t.Parallel()
	source := `def run(name, retries)
  add 1, 2
  configure name: "Ada", retries: 3
  configure name:, retries:
  accept a: 1, b: 2
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "add"},
				Args: []ast.Expression{
					&ast.IntegerLiteral{Value: 1},
					&ast.IntegerLiteral{Value: 2},
				},
				KwArgs: []ast.KeywordArg{},
			},
		},
		&ast.ExprStmt{
			Expr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "configure"},
				Args:   []ast.Expression{},
				KwArgs: []ast.KeywordArg{
					{Name: "name", Value: &ast.StringLiteral{Value: "Ada"}},
					{Name: "retries", Value: &ast.IntegerLiteral{Value: 3}},
				},
				KeywordOptionsHash: true,
			},
		},
		&ast.ExprStmt{
			Expr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "configure"},
				Args:   []ast.Expression{},
				KwArgs: []ast.KeywordArg{
					{Name: "name", Value: &ast.Identifier{Name: "name"}},
					{Name: "retries", Value: &ast.Identifier{Name: "retries"}},
				},
				KeywordOptionsHash: true,
			},
		},
		&ast.ExprStmt{
			Expr: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "accept"},
				Args:   []ast.Expression{},
				KwArgs: []ast.KeywordArg{
					{Name: "a", Value: &ast.IntegerLiteral{Value: 1}},
					{Name: "b", Value: &ast.IntegerLiteral{Value: 2}},
				},
				KeywordOptionsHash: true,
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserParenlessCallDoesNotContinueCommaAcrossLine(t *testing.T) {
	t.Parallel()
	source := `def run
  add 1,
  2
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = nil, want newline-separated comma diagnostic", source)
	}
}

func TestParserParenlessCallDoesNotStealExistingCallOrIndexSyntax(t *testing.T) {
	t.Parallel()
	source := `def run
  value = id(1)
  item = items[0]
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "value"},
			Value: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "id"},
				Args:   []ast.Expression{&ast.IntegerLiteral{Value: 1}},
				KwArgs: []ast.KeywordArg{},
			},
		},
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "item"},
			Value: &ast.IndexExpr{
				Object: &ast.Identifier{Name: "items"},
				Index:  &ast.IntegerLiteral{Value: 0},
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

func TestParserLineInitialSplatAssignmentStartsNewStatement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		assignment string
		wantTarget *ast.DestructureTarget
	}{
		{
			name:       "anonymous rest",
			assignment: "*, last = values",
			wantTarget: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Rest: true},
				{Target: &ast.Identifier{Name: "last"}},
			}},
		},
		{
			name:       "named rest",
			assignment: "*rest, last = values",
			wantTarget: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "rest"}, Rest: true},
				{Target: &ast.Identifier{Name: "last"}},
			}},
		},
		{
			name:       "bare named rest",
			assignment: "*rest = values",
			wantTarget: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "rest"}, Rest: true},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := "def run\n  a = 3\n  " + tt.assignment + "\nend"
			got, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
			}

			wantBody := []ast.Statement{
				&ast.AssignStmt{
					Target: &ast.Identifier{Name: "a"},
					Value:  &ast.IntegerLiteral{Value: 3},
				},
				&ast.AssignStmt{
					Target: tt.wantTarget,
					Value:  &ast.Identifier{Name: "values"},
				},
			}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParserLineInitialAsteriskContinuesMultiplication(t *testing.T) {
	t.Parallel()
	source := `def run
  a = 3
  b = 4
  x = a
  * b
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "a"},
			Value:  &ast.IntegerLiteral{Value: 3},
		},
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "b"},
			Value:  &ast.IntegerLiteral{Value: 4},
		},
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "x"},
			Value: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "a"},
				Operator: ast.TokenAsterisk,
				Right:    &ast.Identifier{Name: "b"},
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

func TestParserMinusOperatorLineContinuesAcrossNewline(t *testing.T) {
	t.Parallel()
	source := `def run
  x = 10
    -
    3
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

func TestParserQuestionOperatorLineContinuesAcrossNewline(t *testing.T) {
	t.Parallel()
	source := `def run(flag)
  value = flag
    ? 1
    : 2
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "value"},
			Value: &ast.ConditionalExpr{
				Condition:  &ast.Identifier{Name: "flag"},
				Consequent: &ast.IntegerLiteral{Value: 1},
				Alternate:  &ast.IntegerLiteral{Value: 2},
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

func TestParserSemicolonStatementSeparators(t *testing.T) {
	t.Parallel()
	source := `def run
  x = 1; y = 2; x + y
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "x"},
			Value:  &ast.IntegerLiteral{Value: 1},
		},
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "y"},
			Value:  &ast.IntegerLiteral{Value: 2},
		},
		&ast.ExprStmt{
			Expr: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "x"},
				Operator: ast.TokenPlus,
				Right:    &ast.Identifier{Name: "y"},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserSemicolonControlFlowSeparators(t *testing.T) {
	t.Parallel()
	source := `def run(flag, values)
  if flag; 1; else; 2; end
  while flag; break; end
  for value in values; value; end
  case flag; when true; 3; else; 4; end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.IfStmt{
			Condition: &ast.Identifier{Name: "flag"},
			Consequent: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 1}},
			},
			Alternate: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 2}},
			},
		},
		&ast.WhileStmt{
			Condition: &ast.Identifier{Name: "flag"},
			Body: []ast.Statement{
				&ast.BreakStmt{},
			},
		},
		&ast.ForStmt{
			Iterator: "value",
			Iterable: &ast.Identifier{
				Name: "values",
			},
			Body: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.Identifier{Name: "value"}},
			},
		},
		&ast.ExprStmt{
			Expr: &ast.CaseExpr{
				Target: &ast.Identifier{Name: "flag"},
				Clauses: []ast.CaseWhenClause{
					{
						Values: []ast.Expression{&ast.BoolLiteral{Value: true}},
						Result: &ast.IntegerLiteral{Value: 3},
					},
				},
				ElseExpr: &ast.IntegerLiteral{Value: 4},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserThenControlFlowSeparators(t *testing.T) {
	t.Parallel()
	source := `def run(value)
  if value == 1 then "one" elsif value == 2 then "two" else "other" end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.IfStmt{
			Condition: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "value"},
				Operator: ast.TokenEQ,
				Right:    &ast.IntegerLiteral{Value: 1},
			},
			Consequent: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.StringLiteral{Value: "one"}},
			},
			ElseIf: []*ast.IfStmt{
				{
					Condition: &ast.BinaryExpr{
						Left:     &ast.Identifier{Name: "value"},
						Operator: ast.TokenEQ,
						Right:    &ast.IntegerLiteral{Value: 2},
					},
					Consequent: []ast.Statement{
						&ast.ExprStmt{Expr: &ast.StringLiteral{Value: "two"}},
					},
				},
			},
			Alternate: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.StringLiteral{Value: "other"}},
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
