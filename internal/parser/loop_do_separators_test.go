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
