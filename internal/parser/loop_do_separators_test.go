package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserLoopDoSeparators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		wantBody []ast.Statement
	}{
		{
			name: "while_with_do",
			source: `def run
  while i < 2 do
    i = i + 1
  end
end`,
			wantBody: []ast.Statement{whileIncrementStatement()},
		},
		{
			name: "while_without_do",
			source: `def run
  while i < 2
    i = i + 1
  end
end`,
			wantBody: []ast.Statement{whileIncrementStatement()},
		},
		{
			name: "until_with_do",
			source: `def run
  until i >= 2 do
    i = i + 1
  end
end`,
			wantBody: []ast.Statement{untilIncrementStatement()},
		},
		{
			name: "until_without_do",
			source: `def run
  until i >= 2
    i = i + 1
  end
end`,
			wantBody: []ast.Statement{untilIncrementStatement()},
		},
		{
			name: "for_with_do",
			source: `def run
  for item in items do
    total = total + item
  end
end`,
			wantBody: []ast.Statement{forSumStatement()},
		},
		{
			name: "for_without_do",
			source: `def run
  for item in items
    total = total + item
  end
end`,
			wantBody: []ast.Statement{forSumStatement()},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}

			if diff := cmp.Diff(tc.wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParserLoopDoSeparatorsAfterCalls(t *testing.T) {
	t.Parallel()

	source := `def run
  while ready(1) do
    tick
  end

  until done(2) do
    tick
  end

  for item in range(1, 3) do
    tick
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.WhileStmt{
			Condition: callExpression("ready", &ast.IntegerLiteral{Value: 1}),
			Body: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.Identifier{Name: "tick"}},
			},
		},
		&ast.UntilStmt{
			Condition: callExpression("done", &ast.IntegerLiteral{Value: 2}),
			Body: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.Identifier{Name: "tick"}},
			},
		},
		&ast.ForStmt{
			Iterator: "item",
			Iterable: callExpression(
				"range",
				&ast.IntegerLiteral{Value: 1},
				&ast.IntegerLiteral{Value: 3},
			),
			Body: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.Identifier{Name: "tick"}},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func whileIncrementStatement() ast.Statement {
	return &ast.WhileStmt{
		Condition: &ast.BinaryExpr{
			Left:     &ast.Identifier{Name: "i"},
			Operator: ast.TokenLT,
			Right:    &ast.IntegerLiteral{Value: 2},
		},
		Body: []ast.Statement{incrementIStatement()},
	}
}

func callExpression(name string, args ...ast.Expression) ast.Expression {
	return &ast.CallExpr{
		Callee: &ast.Identifier{Name: name},
		Args:   args,
		KwArgs: []ast.KeywordArg{},
	}
}

func untilIncrementStatement() ast.Statement {
	return &ast.UntilStmt{
		Condition: &ast.BinaryExpr{
			Left:     &ast.Identifier{Name: "i"},
			Operator: ast.TokenGTE,
			Right:    &ast.IntegerLiteral{Value: 2},
		},
		Body: []ast.Statement{incrementIStatement()},
	}
}

func incrementIStatement() ast.Statement {
	return &ast.AssignStmt{
		Target: &ast.Identifier{Name: "i"},
		Value: &ast.BinaryExpr{
			Left:     &ast.Identifier{Name: "i"},
			Operator: ast.TokenPlus,
			Right:    &ast.IntegerLiteral{Value: 1},
		},
	}
}

func forSumStatement() ast.Statement {
	return &ast.ForStmt{
		Iterator: "item",
		Iterable: &ast.Identifier{
			Name: "items",
		},
		Body: []ast.Statement{
			&ast.AssignStmt{
				Target: &ast.Identifier{Name: "total"},
				Value: &ast.BinaryExpr{
					Left:     &ast.Identifier{Name: "total"},
					Operator: ast.TokenPlus,
					Right:    &ast.Identifier{Name: "item"},
				},
			},
		},
	}
}
