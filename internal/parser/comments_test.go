package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestLexerSkipsRubyBlockComments(t *testing.T) {
	t.Parallel()

	source := "before\n=begin\nignored\n=end\nafter\n"
	got := tokenTypes(source)
	want := []ast.TokenType{ast.TokenIdent, ast.TokenIdent, ast.TokenEOF}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("tokenTypes(%q) mismatch (-want +got):\n%s", source, diff)
	}
}

func TestLexerBlockCommentMarkersMustStartLine(t *testing.T) {
	t.Parallel()

	source := "value =begin\n=end\n"
	got := tokenTypes(source)
	want := []ast.TokenType{
		ast.TokenIdent,
		ast.TokenAssign,
		ast.TokenBegin,
		ast.TokenAssign,
		ast.TokenEnd,
		ast.TokenEOF,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("tokenTypes(%q) mismatch (-want +got):\n%s", source, diff)
	}
}

func TestLexerReportsUnterminatedRubyBlockComment(t *testing.T) {
	t.Parallel()

	source := "=begin\nignored\n"
	tok := newLexer(source).NextToken()
	if tok.Type != ast.TokenIllegal || tok.Literal != "unterminated block comment" {
		t.Fatalf("NextToken(%q) = (%s, %q), want (%s, %q)", source, tok.Type, tok.Literal, ast.TokenIllegal, "unterminated block comment")
	}
}

func TestParserSkipsRubyBlockComments(t *testing.T) {
	t.Parallel()

	source := `def run
  before = 1
=begin
ignored
=end
  after = 2
  =begin
  indented ignored
  =end
  before + after
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "before"},
			Value:  &ast.IntegerLiteral{Value: 1},
		},
		&ast.AssignStmt{
			Target: &ast.Identifier{Name: "after"},
			Value:  &ast.IntegerLiteral{Value: 2},
		},
		&ast.ExprStmt{
			Expr: &ast.BinaryExpr{
				Left:     &ast.Identifier{Name: "before"},
				Operator: ast.TokenPlus,
				Right:    &ast.Identifier{Name: "after"},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func tokenTypes(source string) []ast.TokenType {
	lex := newLexer(source)
	var tokens []ast.TokenType
	for {
		tok := lex.NextToken()
		tokens = append(tokens, tok.Type)
		if tok.Type == ast.TokenEOF {
			return tokens
		}
	}
}
