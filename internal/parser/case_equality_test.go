package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserCaseEqualityExpression(t *testing.T) {
	t.Parallel()

	source := `def run
  matcher === value
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "matcher"},
				Operator: ast.TokenCaseEQ,
				Right:    &ast.Identifier{Name: "value"},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserCaseEqualityContinuesAcrossNewline(t *testing.T) {
	t.Parallel()

	source := `def run
  result = matcher
    ===
    value
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "result"},
			Value: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "matcher"},
				Operator: ast.TokenCaseEQ,
				Right:    &ast.Identifier{Name: "value"},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// TestLexerDisambiguatesEqualitySigils pins the maximal-munch rule for `=` runs:
// `==` lexes as equality, `===` as case equality, and a longer run greedily
// consumes `===` before falling back to a bare assignment token.
func TestLexerDisambiguatesEqualitySigils(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []ast.TokenType
	}{
		{
			name:   "double_equals_is_equality",
			source: "==",
			want:   []ast.TokenType{ast.TokenEQ, ast.TokenEOF},
		},
		{
			name:   "triple_equals_is_case_equality",
			source: "===",
			want:   []ast.TokenType{ast.TokenCaseEQ, ast.TokenEOF},
		},
		{
			name:   "quad_equals_is_case_equality_then_assign",
			source: "====",
			want:   []ast.TokenType{ast.TokenCaseEQ, ast.TokenAssign, ast.TokenEOF},
		},
		{
			name:   "case_equality_with_operands",
			source: "a === b",
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenCaseEQ, ast.TokenIdent, ast.TokenEOF},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lex := newLexer(tc.source)
			var got []ast.TokenType
			for {
				tok := lex.NextToken()
				got = append(got, tok.Type)
				if tok.Type == ast.TokenEOF {
					break
				}
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("token stream mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
