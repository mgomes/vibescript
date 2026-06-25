package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserUnaryPlus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   ast.Expression
	}{
		{
			name:   "integer literal",
			source: "+1",
			want: &ast.UnaryExpr{
				Operator: ast.TokenPlus,
				Right:    &ast.IntegerLiteral{Value: 1},
			},
		},
		{
			name:   "float literal",
			source: "+1.5",
			want: &ast.UnaryExpr{
				Operator: ast.TokenPlus,
				Right:    &ast.FloatLiteral{Value: 1.5},
			},
		},
		{
			name:   "string literal",
			source: `+"x"`,
			want: &ast.UnaryExpr{
				Operator: ast.TokenPlus,
				Right:    &ast.StringLiteral{Value: "x"},
			},
		},
		{
			name:   "identifier",
			source: "+x",
			want: &ast.UnaryExpr{
				Operator: ast.TokenPlus,
				Right:    &ast.Identifier{Name: "x"},
			},
		},
		{
			name:   "parenthesized expression",
			source: "+(3 + 4)",
			want: &ast.UnaryExpr{
				Operator: ast.TokenPlus,
				Right: &ast.BinaryExpr{
					Left:     &ast.IntegerLiteral{Value: 3},
					Operator: ast.TokenPlus,
					Right:    &ast.IntegerLiteral{Value: 4},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseSingleExpr(t, tt.source)
			if diff := cmp.Diff(tt.want, got, astCmpOpts); diff != "" {
				t.Fatalf("parseSource(%q) expression mismatch (-want +got):\n%s", tt.source, diff)
			}
		})
	}
}

// TestParserBinaryPlusUnaffected guards that adding prefix `+` does not change
// how a `+` between two operands parses: it must remain binary addition rather
// than collapsing into a parenless call argument.
func TestParserBinaryPlusUnaffected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   ast.Expression
	}{
		{
			name:   "literal operands",
			source: "1 + 2",
			want: &ast.BinaryExpr{
				Left:     &ast.IntegerLiteral{Value: 1},
				Operator: ast.TokenPlus,
				Right:    &ast.IntegerLiteral{Value: 2},
			},
		},
		{
			name:   "identifier operands stay subtraction-like binary form",
			source: "foo + bar",
			want: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "foo"},
				Operator: ast.TokenPlus,
				Right:    &ast.Identifier{Name: "bar"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseSingleExpr(t, tt.source)
			if diff := cmp.Diff(tt.want, got, astCmpOpts); diff != "" {
				t.Fatalf("parseSource(%q) expression mismatch (-want +got):\n%s", tt.source, diff)
			}
		})
	}
}

// TestParserUnaryPlusLineDisambiguation covers how a leading `+` at the start of
// a fresh line is parsed. When it sits flush against its operand it begins a new
// statement (matching Ruby). With an intervening space it continues the previous
// expression as binary addition via Vibescript's indented-continuation rule;
// this spaced form intentionally differs from Ruby, which would instead parse
// the second line as a separate `+1` unary-plus statement.
func TestParserUnaryPlusLineDisambiguation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		source     string
		wantStmts  int
		wantFirst  ast.Expression
		wantSecond ast.Expression
	}{
		{
			name: "flush sign starts a new statement",
			source: `x = 5
+x`,
			wantStmts: 2,
			wantSecond: &ast.UnaryExpr{
				Operator: ast.TokenPlus,
				Right:    &ast.Identifier{Name: "x"},
			},
		},
		{
			name:      "spaced sign continues the line",
			source:    "5\n + 1",
			wantStmts: 1,
			wantFirst: &ast.BinaryExpr{
				Left:     &ast.IntegerLiteral{Value: 5},
				Operator: ast.TokenPlus,
				Right:    &ast.IntegerLiteral{Value: 1},
			},
		},
		{
			// Pins the documented flush example `total\n+amount`: the leading
			// `+` sits flush against `amount`, so the second line is its own
			// `+amount` statement rather than continuing `total`. This matches
			// Ruby, which also treats the flush form as a new statement.
			name: "flush doc example parses as two statements",
			source: `total
+amount`,
			wantStmts: 2,
			wantFirst: &ast.Identifier{Name: "total"},
			wantSecond: &ast.UnaryExpr{
				Operator: ast.TokenPlus,
				Right:    &ast.Identifier{Name: "amount"},
			},
		},
		{
			// Pins the documented spaced example `total\n + amount`: the space
			// before `amount` triggers Vibescript's indented-continuation rule,
			// so the two lines join into binary addition. This intentionally
			// differs from Ruby, which would parse the second line as a separate
			// `+amount` statement.
			name: "spaced doc example continues as binary addition",
			source: `total
 + amount`,
			wantStmts: 1,
			wantFirst: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "total"},
				Operator: ast.TokenPlus,
				Right:    &ast.Identifier{Name: "amount"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			program, errs := parseSource(t, tt.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tt.source, errs)
			}
			if len(program.Statements) != tt.wantStmts {
				t.Fatalf("parseSource(%q) returned %d statements, want %d", tt.source, len(program.Statements), tt.wantStmts)
			}
			if tt.wantFirst != nil {
				first := exprStmt(t, program.Statements[0])
				if diff := cmp.Diff(tt.wantFirst, first, astCmpOpts); diff != "" {
					t.Fatalf("parseSource(%q) first statement mismatch (-want +got):\n%s", tt.source, diff)
				}
			}
			if tt.wantSecond != nil {
				second := exprStmt(t, program.Statements[1])
				if diff := cmp.Diff(tt.wantSecond, second, astCmpOpts); diff != "" {
					t.Fatalf("parseSource(%q) second statement mismatch (-want +got):\n%s", tt.source, diff)
				}
			}
		})
	}
}

// exprStmt extracts the expression from an expression statement.
func exprStmt(t testing.TB, stmt ast.Statement) ast.Expression {
	t.Helper()
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		t.Fatalf("statement = %T, want *ast.ExprStmt", stmt)
	}
	return expr.Expr
}

// parseSingleExpr parses source expected to contain exactly one expression
// statement and returns its expression.
func parseSingleExpr(t testing.TB, source string) ast.Expression {
	t.Helper()
	program, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	if len(program.Statements) != 1 {
		t.Fatalf("parseSource(%q) returned %d statements, want 1", source, len(program.Statements))
	}
	stmt, ok := program.Statements[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("parseSource(%q) statement = %T, want *ast.ExprStmt", source, program.Statements[0])
	}
	return stmt.Expr
}
