package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mgomes/vibescript/internal/ast"
)

// A minus before a numeric literal folds into the literal, so a member call
// binds to the negative value the way Ruby binds it: -5.abs is 5, not
// -(5.abs). Before this the operand was parsed at precPrefix, below precCall,
// so the member access was swallowed into the operand and .abs silently
// returned a negative number.
//
// This pins the parse shape; runtime_unary_minus_literal_test.go pins the
// values, since the shape alone does not prove the precedence is right.
func TestParserFoldsMinusIntoNumericLiteral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want ast.Expression
	}{
		{
			name: "integer literal folds",
			expr: "-5",
			want: &ast.IntegerLiteral{Value: -5},
		},
		{
			name: "float literal folds",
			expr: "-1.5",
			want: &ast.FloatLiteral{Value: -1.5},
		},
		{
			// The member call attaches to the negative literal rather than the
			// sign attaching to the call's result.
			name: "member call binds to the negative literal",
			expr: "-5.abs",
			want: &ast.MemberExpr{
				Object:   &ast.IntegerLiteral{Value: -5},
				Property: "abs",
			},
		},
		{
			// Exponentiation binds tighter than a literal's sign in Ruby, so
			// this must keep the unary form: -(2 ** 2) is -4, not (-2) ** 2.
			name: "exponent keeps the unary form",
			expr: "-2 ** 2",
			want: &ast.UnaryExpr{
				Operator: ast.TokenMinus,
				Right: &ast.BinaryExpr{
					Left:     &ast.IntegerLiteral{Value: 2},
					Operator: ast.TokenPower,
					Right:    &ast.IntegerLiteral{Value: 2},
				},
			},
		},
		{
			// Only literals fold. A variable keeps the unary form, so -x.abs
			// stays -(x.abs), matching Ruby.
			name: "identifier does not fold",
			expr: "-x.abs",
			want: &ast.UnaryExpr{
				Operator: ast.TokenMinus,
				Right: &ast.MemberExpr{
					Object:   &ast.Identifier{Name: "x"},
					Property: "abs",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, errs := parseSource(t, "def run(x)\n  "+tc.expr+"\nend")
			if len(errs) > 0 {
				t.Fatalf("expected no parse errors, got %v", errs)
			}
			body := parsedFunctionBody(t, got)
			if len(body) != 1 {
				t.Fatalf("expected one statement, got %d", len(body))
			}
			stmt, ok := body[0].(*ast.ExprStmt)
			if !ok {
				t.Fatalf("expected an expression statement, got %T", body[0])
			}
			if diff := cmp.Diff(tc.want, stmt.Expr, astCmpOpts); diff != "" {
				t.Fatalf("%s mismatch (-want +got):\n%s", tc.name, diff)
			}
		})
	}
}
