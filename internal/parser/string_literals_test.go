package parser

import (
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestSingleQuotedStringLiterals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "plain",
			source: "'hello'",
			want:   "hello",
		},
		{
			name:   "escaped_quote",
			source: `'don\'t'`,
			want:   "don't",
		},
		{
			name:   "escaped_backslash",
			source: `'c:\\tmp'`,
			want:   `c:\tmp`,
		},
		{
			name:   "non_special_escape_stays_literal",
			source: `'a\nb'`,
			want:   `a\nb`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			program, errs := parseSource(t, "def run\n  "+tc.source+"\nend\n")
			if len(errs) != 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}
			if len(program.Statements) != 1 {
				t.Fatalf("parseSource(%q) statements = %d, want 1", tc.source, len(program.Statements))
			}
			fn, ok := program.Statements[0].(*ast.FunctionStmt)
			if !ok {
				t.Fatalf("parseSource(%q) statement = %T, want *ast.FunctionStmt", tc.source, program.Statements[0])
			}
			if len(fn.Body) != 1 {
				t.Fatalf("parseSource(%q) function body length = %d, want 1", tc.source, len(fn.Body))
			}
			stmt, ok := fn.Body[0].(*ast.ExprStmt)
			if !ok {
				t.Fatalf("parseSource(%q) body[0] = %T, want *ast.ExprStmt", tc.source, fn.Body[0])
			}
			lit, ok := stmt.Expr.(*ast.StringLiteral)
			if !ok {
				t.Fatalf("parseSource(%q) expression = %T, want *ast.StringLiteral", tc.source, stmt.Expr)
			}
			if lit.Value != tc.want {
				t.Fatalf("parseSource(%q) literal value = %q, want %q", tc.source, lit.Value, tc.want)
			}
		})
	}
}
