package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserSymbolicBooleanOperators(t *testing.T) {
	t.Parallel()

	source := `def run
  !false && true || false
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.BinaryExpr{
				Left: &ast.BinaryExpr{
					Left: &ast.UnaryExpr{
						Operator: ast.TokenBang,
						Right:    &ast.BoolLiteral{Value: false},
					},
					Operator: ast.TokenAnd,
					Right:    &ast.BoolLiteral{Value: true},
				},
				Operator: ast.TokenOr,
				Right:    &ast.BoolLiteral{Value: false},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserWordBooleanOperators(t *testing.T) {
	t.Parallel()

	source := `def run
  not allowed user and fallback or final
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.LogicalStmt{
			Left: &ast.LogicalStmt{
				Left: &ast.ExprStmt{
					Expr: &ast.UnaryExpr{
						Operator: ast.TokenNot,
						Right: &ast.CallExpr{
							Callee: &ast.Identifier{Name: "allowed"},
							Args: []ast.Expression{
								&ast.Identifier{Name: "user"},
							},
							KwArgs: []ast.KeywordArg{},
						},
					},
				},
				Operator: ast.TokenWordAnd,
				Right:    &ast.ExprStmt{Expr: &ast.Identifier{Name: "fallback"}},
			},
			Operator: ast.TokenWordOr,
			Right:    &ast.ExprStmt{Expr: &ast.Identifier{Name: "final"}},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserPlainExpressionGuardFormsSplitAtWordOperators(t *testing.T) {
	t.Parallel()

	source := `def run
  ready or raise "not ready"
  ready or fallback = 1
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.LogicalStmt{
			Left:     &ast.ExprStmt{Expr: &ast.Identifier{Name: "ready"}},
			Operator: ast.TokenWordOr,
			Right:    &ast.RaiseStmt{Value: &ast.StringLiteral{Value: "not ready"}},
		},
		&ast.LogicalStmt{
			Left:     &ast.ExprStmt{Expr: &ast.Identifier{Name: "ready"}},
			Operator: ast.TokenWordOr,
			Right: &ast.AssignStmt{
				Target: &ast.Identifier{Name: "fallback"},
				Value:  &ast.IntegerLiteral{Value: 1},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserModifierGuardsWholeLogicalStatement(t *testing.T) {
	t.Parallel()

	source := `def run(condition)
  ready or fallback if condition
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.IfStmt{
			Condition: &ast.Identifier{Name: "condition"},
			Consequent: []ast.Statement{
				&ast.LogicalStmt{
					Left:     &ast.ExprStmt{Expr: &ast.Identifier{Name: "ready"}},
					Operator: ast.TokenWordOr,
					Right:    &ast.ExprStmt{Expr: &ast.Identifier{Name: "fallback"}},
				},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserWordBooleanOperatorsInsideLineLimitedNestedExpressions(t *testing.T) {
	t.Parallel()

	source := `def run
  x = (true and false)
  y = [a or b]
  z = {ok: c and d}
  call(e or f)
end`

	_, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
}

func TestParserStatementWordBooleanPrecedence(t *testing.T) {
	t.Parallel()

	source := `def run
  x = false and y = true or z = true
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.LogicalStmt{
			Left: &ast.LogicalStmt{
				Left: &ast.AssignStmt{
					Target: &ast.Identifier{Name: "x"},
					Value:  &ast.BoolLiteral{Value: false},
				},
				Operator: ast.TokenWordAnd,
				Right: &ast.AssignStmt{
					Target: &ast.Identifier{Name: "y"},
					Value:  &ast.BoolLiteral{Value: true},
				},
			},
			Operator: ast.TokenWordOr,
			Right: &ast.AssignStmt{
				Target: &ast.Identifier{Name: "z"},
				Value:  &ast.BoolLiteral{Value: true},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserRejectsSymbolicBooleanLabels(t *testing.T) {
	t.Parallel()

	cases := []string{
		`def run
  {&&: 1}
end`,
		`def run
  takes(||: 2)
end`,
	}
	for _, source := range cases {
		_, errs := parseSource(t, source)
		if len(errs) == 0 {
			t.Fatalf("parseSource(%q) errors = none, want symbolic boolean label error", source)
		}
	}
}
