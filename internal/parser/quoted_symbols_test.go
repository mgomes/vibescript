package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

// TestLexerQuotedSymbolLiterals verifies that the lexer decodes quoted symbol
// literals into a single TokenSymbol whose literal is the symbol name, mirroring
// the way quoted strings decode their escapes.
func TestLexerQuotedSymbolLiterals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "double_hyphen", source: `:"foo-bar"`, want: "foo-bar"},
		{name: "double_spaces", source: `:"foo bar"`, want: "foo bar"},
		{name: "double_empty", source: `:""`, want: ""},
		{name: "double_punctuation", source: `:"a+b?"`, want: "a+b?"},
		{name: "double_escaped_quote", source: `:"a\"b"`, want: `a"b`},
		{name: "double_escaped_newline", source: `:"a\nb"`, want: "a\nb"},
		{name: "single_hyphen", source: `:'foo-bar'`, want: "foo-bar"},
		{name: "single_spaces", source: `:'foo bar'`, want: "foo bar"},
		{name: "single_empty", source: `:''`, want: ""},
		{name: "single_interpolation_is_literal", source: `:'a#{b}'`, want: "a#{b}"},
		{name: "single_escaped_quote", source: `:'a\'b'`, want: "a'b"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lex := newLexer(tc.source)
			tok := lex.NextToken()
			if tok.Type != ast.TokenSymbol {
				t.Fatalf("NextToken(%q) type = %v, want %v", tc.source, tok.Type, ast.TokenSymbol)
			}
			if tok.Literal != tc.want {
				t.Fatalf("NextToken(%q) literal = %q, want %q", tc.source, tok.Literal, tc.want)
			}
			if next := lex.NextToken(); next.Type != ast.TokenEOF {
				t.Fatalf("NextToken(%q) trailing token = %v, want EOF", tc.source, next.Type)
			}
		})
	}
}

// TestParserQuotedSymbolLiterals verifies that quoted symbols parse into the
// same SymbolLiteral node as bare symbols, anywhere a symbol literal is allowed.
func TestParserQuotedSymbolLiterals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []ast.Statement
	}{
		{
			name:   "primary_expression",
			source: "def run\n  :\"foo-bar\"\nend",
			want: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.SymbolLiteral{Name: "foo-bar"}},
			},
		},
		{
			name:   "single_quoted",
			source: "def run\n  :'foo bar'\nend",
			want: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.SymbolLiteral{Name: "foo bar"}},
			},
		},
		{
			name:   "in_array",
			source: "def run\n  [:\"a-b\", :c, :\"d e\"]\nend",
			want: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.ArrayLiteral{Elements: []ast.Expression{
					&ast.SymbolLiteral{Name: "a-b"},
					&ast.SymbolLiteral{Name: "c"},
					&ast.SymbolLiteral{Name: "d e"},
				}}},
			},
		},
		{
			name:   "empty_symbol",
			source: "def run\n  :\"\"\nend",
			want: []ast.Statement{
				&ast.ExprStmt{Expr: &ast.SymbolLiteral{Name: ""}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, errs := parseSource(t, tc.source)
			if len(errs) != 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}
			if diff := cmp.Diff(tc.want, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestParserQuotedSymbolInterpolationRejected confirms interpolated quoted
// symbols are rejected: Vibescript supports non-interpolated quoted symbols only,
// matching the scope agreed for the feature.
func TestParserQuotedSymbolInterpolationRejected(t *testing.T) {
	t.Parallel()

	source := "def run\n  :\"foo#{1}\"\nend"
	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = nil, want a parse error", source)
	}
	var got strings.Builder
	for _, err := range errs {
		got.WriteString(err.Error())
		got.WriteByte('\n')
	}
	if !strings.Contains(got.String(), "invalid token") {
		t.Fatalf("parseSource(%q) errors = %s, want substring %q", source, got.String(), "invalid token")
	}
}

// TestParserQuotedSymbolUnterminatedRejected confirms an unterminated quoted
// symbol surfaces a parse error rather than silently consuming the rest of input.
func TestParserQuotedSymbolUnterminatedRejected(t *testing.T) {
	t.Parallel()

	for _, source := range []string{"def run\n  :\"foo\nend", "def run\n  :'foo\nend"} {
		_, errs := parseSource(t, source)
		if len(errs) == 0 {
			t.Fatalf("parseSource(%q) errors = nil, want a parse error", source)
		}
	}
}
