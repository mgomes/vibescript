package parser

import (
	"strings"
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

func TestDoubleQuotedStringInterpolation(t *testing.T) {
	t.Parallel()
	source := `def run
  "hi #{name}, #{score + 1}"
end`

	program, errs := parseSource(t, source)
	if len(errs) != 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	fn, ok := program.Statements[0].(*ast.FunctionStmt)
	if !ok {
		t.Fatalf("statement = %T, want *ast.FunctionStmt", program.Statements[0])
	}
	stmt, ok := fn.Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("body[0] = %T, want *ast.ExprStmt", fn.Body[0])
	}
	lit, ok := stmt.Expr.(*ast.InterpolatedString)
	if !ok {
		t.Fatalf("expression = %T, want *ast.InterpolatedString", stmt.Expr)
	}
	if len(lit.Parts) != 4 {
		t.Fatalf("parts length = %d, want 4", len(lit.Parts))
	}
	if text, ok := lit.Parts[0].(ast.StringText); !ok || text.Text != "hi " {
		t.Fatalf("parts[0] = %#v, want text %q", lit.Parts[0], "hi ")
	}
	if expr, ok := lit.Parts[1].(ast.StringExpr); !ok {
		t.Fatalf("parts[1] = %T, want ast.StringExpr", lit.Parts[1])
	} else if ident, ok := expr.Expr.(*ast.Identifier); !ok || ident.Name != "name" {
		t.Fatalf("parts[1].Expr = %#v, want identifier name", expr.Expr)
	}
	if text, ok := lit.Parts[2].(ast.StringText); !ok || text.Text != ", " {
		t.Fatalf("parts[2] = %#v, want text %q", lit.Parts[2], ", ")
	}
	if expr, ok := lit.Parts[3].(ast.StringExpr); !ok {
		t.Fatalf("parts[3] = %T, want ast.StringExpr", lit.Parts[3])
	} else if binary, ok := expr.Expr.(*ast.BinaryExpr); !ok || binary.Operator != ast.TokenPlus {
		t.Fatalf("parts[3].Expr = %#v, want plus expression", expr.Expr)
	}
}

func TestDoubleQuotedStringInterpolationWithQuotedExpression(t *testing.T) {
	t.Parallel()
	source := `def run
  "#{name || "guest"}"
end`

	program, errs := parseSource(t, source)
	if len(errs) != 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	fn := program.Statements[0].(*ast.FunctionStmt)
	stmt := fn.Body[0].(*ast.ExprStmt)
	lit, ok := stmt.Expr.(*ast.InterpolatedString)
	if !ok {
		t.Fatalf("expression = %T, want *ast.InterpolatedString", stmt.Expr)
	}
	if len(lit.Parts) != 1 {
		t.Fatalf("parts length = %d, want 1", len(lit.Parts))
	}
	expr, ok := lit.Parts[0].(ast.StringExpr)
	if !ok {
		t.Fatalf("parts[0] = %T, want ast.StringExpr", lit.Parts[0])
	}
	binary, ok := expr.Expr.(*ast.BinaryExpr)
	if !ok || binary.Operator != ast.TokenOr {
		t.Fatalf("parts[0].Expr = %#v, want or expression", expr.Expr)
	}
	if right, ok := binary.Right.(*ast.StringLiteral); !ok || right.Value != "guest" {
		t.Fatalf("or right = %#v, want guest string", binary.Right)
	}
}

func TestEscapedDoubleQuotedInterpolationMarkerStaysLiteral(t *testing.T) {
	t.Parallel()
	source := `def run
  "\#{name}"
end`

	program, errs := parseSource(t, source)
	if len(errs) != 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	fn := program.Statements[0].(*ast.FunctionStmt)
	stmt := fn.Body[0].(*ast.ExprStmt)
	lit, ok := stmt.Expr.(*ast.StringLiteral)
	if !ok {
		t.Fatalf("expression = %T, want *ast.StringLiteral", stmt.Expr)
	}
	if lit.Value != "#{name}" {
		t.Fatalf("literal value = %q, want #{name}", lit.Value)
	}
}

func TestEscapedBackslashBeforeInterpolationMarker(t *testing.T) {
	t.Parallel()
	source := `def run
  "\\#{name}"
end`

	program, errs := parseSource(t, source)
	if len(errs) != 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	fn := program.Statements[0].(*ast.FunctionStmt)
	stmt := fn.Body[0].(*ast.ExprStmt)
	lit, ok := stmt.Expr.(*ast.InterpolatedString)
	if !ok {
		t.Fatalf("expression = %T, want *ast.InterpolatedString", stmt.Expr)
	}
	if len(lit.Parts) != 2 {
		t.Fatalf("parts length = %d, want 2", len(lit.Parts))
	}
	if text, ok := lit.Parts[0].(ast.StringText); !ok || text.Text != `\` {
		t.Fatalf("parts[0] = %#v, want backslash text", lit.Parts[0])
	}
	if expr, ok := lit.Parts[1].(ast.StringExpr); !ok {
		t.Fatalf("parts[1] = %T, want ast.StringExpr", lit.Parts[1])
	} else if ident, ok := expr.Expr.(*ast.Identifier); !ok || ident.Name != "name" {
		t.Fatalf("parts[1].Expr = %#v, want identifier name", expr.Expr)
	}
}

func TestStringInterpolationErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "empty",
			source: `def run
  "#{}"
end`,
			want: "empty string interpolation",
		},
		{
			name: "unterminated",
			source: `def run
  "#{name"
end`,
			want: "unterminated string interpolation",
		},
		{
			name: "trailing_tokens",
			source: `def run
  "#{name; other}"
end`,
			want: "string interpolation must contain a single expression",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, tc.source)
			if len(errs) == 0 {
				t.Fatalf("parseSource(%q) errors = nil, want %q", tc.source, tc.want)
			}
			var got strings.Builder
			for _, err := range errs {
				got.WriteString(err.Error())
				got.WriteByte('\n')
			}
			if !strings.Contains(got.String(), tc.want) {
				t.Fatalf("parseSource(%q) errors = %s, want substring %q", tc.source, got.String(), tc.want)
			}
		})
	}
}
