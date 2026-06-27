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

// TestLexerColonQuoteDisambiguation verifies that a colon followed by a quote is
// lexed as a quoted symbol only in expression-start position. When the colon is a
// hash, keyword-argument, or ternary separator that happens to precede a quoted
// string value, it must stay a TokenColon followed by a TokenString so the
// no-space label and ternary forms that predate quoted symbols keep parsing.
func TestLexerColonQuoteDisambiguation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []ast.TokenType
	}{
		{
			name:   "hash_label_no_space_stays_separator",
			source: `{name:"Ada"}`,
			want:   []ast.TokenType{ast.TokenLBrace, ast.TokenIdent, ast.TokenColon, ast.TokenString, ast.TokenRBrace},
		},
		{
			name:   "keyword_argument_no_space_stays_separator",
			source: `call(name:"Ada")`,
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenLParen, ast.TokenIdent, ast.TokenColon, ast.TokenString, ast.TokenRParen},
		},
		{
			name:   "ternary_alternate_no_space_stays_separator",
			source: `flag ? 1 :"no"`,
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenQuestion, ast.TokenInt, ast.TokenColon, ast.TokenString},
		},
		{
			name:   "quoted_string_label_unaffected",
			source: `{"foo-bar": 1}`,
			want:   []ast.TokenType{ast.TokenLBrace, ast.TokenString, ast.TokenColon, ast.TokenInt, ast.TokenRBrace},
		},
		{
			name:   "primary_position_is_symbol",
			source: `:"foo-bar"`,
			want:   []ast.TokenType{ast.TokenSymbol},
		},
		{
			name:   "after_assign_is_symbol",
			source: `x = :"sym"`,
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenAssign, ast.TokenSymbol},
		},
		{
			name:   "after_open_bracket_is_symbol",
			source: `[:"a-b"]`,
			want:   []ast.TokenType{ast.TokenLBracket, ast.TokenSymbol, ast.TokenRBracket},
		},
		{
			name:   "after_comma_is_symbol",
			source: `a, :"b"`,
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenComma, ast.TokenSymbol},
		},
		{
			name:   "after_return_is_symbol",
			source: `return :"x"`,
			want:   []ast.TokenType{ast.TokenReturn, ast.TokenSymbol},
		},
		{
			// Both ternary branches are quoted symbols; the separator colon sits
			// between two symbols, so it must stay a separator while the branches
			// are symbols.
			name:   "ternary_with_symbol_branches",
			source: `flag ? :"a" : :"b"`,
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenQuestion, ast.TokenSymbol, ast.TokenColon, ast.TokenSymbol},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lex := newLexer(tc.source)
			var got []ast.TokenType
			for {
				tok := lex.NextToken()
				if tok.Type == ast.TokenEOF {
					break
				}
				got = append(got, tok.Type)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("token types for %q mismatch (-want +got):\n%s", tc.source, diff)
			}
		})
	}
}

// TestParserColonQuoteSeparatorValues verifies that the no-space label and
// ternary forms whose value starts with a quote parse into the expected nodes,
// not a quoted-symbol misread. These guard the regression where quoted-symbol
// scanning consumed the separator colon.
func TestParserColonQuoteSeparatorValues(t *testing.T) {
	t.Parallel()

	t.Run("hash_label_string_value", func(t *testing.T) {
		t.Parallel()
		want := []ast.Statement{
			&ast.ExprStmt{Expr: &ast.HashLiteral{Pairs: []ast.HashPair{{
				Key:   &ast.SymbolLiteral{Name: "name"},
				Value: &ast.StringLiteral{Value: "Ada"},
			}}}},
		}
		got, errs := parseSource(t, "def run\n  {name:\"Ada\"}\nend")
		if len(errs) != 0 {
			t.Fatalf("parseSource errors = %v, want none", errs)
		}
		if diff := cmp.Diff(want, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
			t.Fatalf("function body mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("keyword_argument_string_value", func(t *testing.T) {
		t.Parallel()
		got, errs := parseSource(t, "def run\n  greet(name:\"Ada\")\nend")
		if len(errs) != 0 {
			t.Fatalf("parseSource errors = %v, want none", errs)
		}
		body := parsedFunctionBody(t, got)
		call := body[0].(*ast.ExprStmt).Expr.(*ast.CallExpr)
		if len(call.Args) != 0 {
			t.Fatalf("call positional args = %d, want 0", len(call.Args))
		}
		if len(call.KwArgs) != 1 {
			t.Fatalf("call keyword args = %d, want 1", len(call.KwArgs))
		}
		if call.KwArgs[0].Name != "name" {
			t.Fatalf("keyword name = %q, want %q", call.KwArgs[0].Name, "name")
		}
		str, ok := call.KwArgs[0].Value.(*ast.StringLiteral)
		if !ok || str.Value != "Ada" {
			t.Fatalf("keyword value = %#v, want StringLiteral(Ada)", call.KwArgs[0].Value)
		}
	})

	t.Run("ternary_string_alternate", func(t *testing.T) {
		t.Parallel()
		got, errs := parseSource(t, "def run\n  flag ? 1 :\"no\"\nend")
		if len(errs) != 0 {
			t.Fatalf("parseSource errors = %v, want none", errs)
		}
		body := parsedFunctionBody(t, got)
		cond, ok := body[0].(*ast.ExprStmt).Expr.(*ast.ConditionalExpr)
		if !ok {
			t.Fatalf("top expression = %T, want *ast.ConditionalExpr", body[0].(*ast.ExprStmt).Expr)
		}
		str, ok := cond.Alternate.(*ast.StringLiteral)
		if !ok || str.Value != "no" {
			t.Fatalf("ternary alternate = %#v, want StringLiteral(no)", cond.Alternate)
		}
	})
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
	const wantMsg = "interpolation is not allowed in a symbol literal"
	if !strings.Contains(got.String(), wantMsg) {
		t.Fatalf("parseSource(%q) errors = %s, want substring %q", source, got.String(), wantMsg)
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
