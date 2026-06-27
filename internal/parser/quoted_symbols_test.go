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
			// A parenless call passing a quoted symbol: the colon follows an
			// identifier that can end an expression, but the whitespace before
			// it rules out a label separator, so it introduces a symbol.
			name:   "parenless_call_argument_is_symbol",
			source: `emit :"foo-bar"`,
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenSymbol},
		},
		{
			// The same parenless-call form on a method receiver.
			name:   "parenless_method_call_argument_is_symbol",
			source: `obj.emit :"foo-bar"`,
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenDot, ast.TokenIdent, ast.TokenSymbol},
		},
		{
			// A value that ends an expression but cannot name a label still
			// reads an abutting colon-quote as a symbol, matching Ruby.
			name:   "value_abutting_colon_quote_is_symbol",
			source: `foo():"x"`,
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenLParen, ast.TokenRParen, ast.TokenSymbol},
		},
		{
			// A line-leading ternary alternate whose value abuts the colon keeps
			// the colon as the ternary separator across the line break, rather
			// than restarting the line as a quoted symbol.
			name:   "line_leading_ternary_alternate_stays_separator",
			source: "flag ?\n  1\n  :\"no\"",
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenQuestion, ast.TokenInt, ast.TokenColon, ast.TokenString},
		},
		{
			// An abutting ternary separator (no space before the colon) is still
			// a separator while a ternary is open.
			name:   "abutting_ternary_alternate_stays_separator",
			source: `flag ? 1:"no"`,
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenQuestion, ast.TokenInt, ast.TokenColon, ast.TokenString},
		},
		{
			// A label-capable keyword whose colon abuts it (no space) is a label
			// separator, not a quoted-symbol introducer, so {rescue:"x"} keeps
			// parsing as a string-valued label.
			name:   "keyword_label_no_space_stays_separator",
			source: `{rescue:"x"}`,
			want:   []ast.TokenType{ast.TokenLBrace, ast.TokenRescue, ast.TokenColon, ast.TokenString, ast.TokenRBrace},
		},
		{
			name:   "keyword_argument_keyword_name_no_space_stays_separator",
			source: `call(begin:"x")`,
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenLParen, ast.TokenBegin, ast.TokenColon, ast.TokenString, ast.TokenRParen},
		},
		{
			// A space before the colon restores the keyword + quoted-symbol
			// reading, matching how Ruby distinguishes return :"x" from return:"x".
			name:   "keyword_then_spaced_symbol_is_symbol",
			source: `return :"x"`,
			want:   []ast.TokenType{ast.TokenReturn, ast.TokenSymbol},
		},
		{
			name:   "word_boolean_keyword_label_no_space_stays_separator",
			source: `{and:"x"}`,
			want:   []ast.TokenType{ast.TokenLBrace, ast.TokenAnd, ast.TokenColon, ast.TokenString, ast.TokenRBrace},
		},
		{
			// Both ternary branches are quoted symbols; the separator colon sits
			// between two symbols, so it must stay a separator while the branches
			// are symbols.
			name:   "ternary_with_symbol_branches",
			source: `flag ? :"a" : :"b"`,
			want:   []ast.TokenType{ast.TokenIdent, ast.TokenQuestion, ast.TokenSymbol, ast.TokenColon, ast.TokenSymbol},
		},
		{
			// A hash-literal consequent introduces a label colon one nesting
			// level deeper than the ternary `?`. That inner colon must not be
			// mistaken for the ternary separator, so the outer abutting
			// colon-quote stays the separator + string rather than a symbol.
			name:   "ternary_hash_branch_then_quoted_alternate",
			source: `flag ? {a: 1} :"no"`,
			want: []ast.TokenType{
				ast.TokenIdent, ast.TokenQuestion, ast.TokenLBrace, ast.TokenIdent, ast.TokenColon,
				ast.TokenInt, ast.TokenRBrace, ast.TokenColon, ast.TokenString,
			},
		},
		{
			// The same with multiple spaced labels in the consequent: every inner
			// label colon sits deeper than the ternary, so none of them pops it
			// and the outer colon-quote stays the separator.
			name:   "ternary_multi_label_hash_branch_then_quoted_alternate",
			source: `flag ? {a: 1, b: 2} :"no"`,
			want: []ast.TokenType{
				ast.TokenIdent, ast.TokenQuestion, ast.TokenLBrace, ast.TokenIdent, ast.TokenColon,
				ast.TokenInt, ast.TokenComma, ast.TokenIdent, ast.TokenColon, ast.TokenInt,
				ast.TokenRBrace, ast.TokenColon, ast.TokenString,
			},
		},
		{
			// A no-space label whose value is a quoted string sits deeper than the
			// ternary: the inner colon stays a label separator (not a quoted
			// symbol), and the outer abutting colon-quote stays the ternary
			// separator + string.
			name:   "ternary_no_space_string_label_hash_branch_then_quoted_alternate",
			source: `flag ? {a:"x"} :"no"`,
			want: []ast.TokenType{
				ast.TokenIdent, ast.TokenQuestion, ast.TokenLBrace, ast.TokenIdent, ast.TokenColon,
				ast.TokenString, ast.TokenRBrace, ast.TokenColon, ast.TokenString,
			},
		},
		{
			// An array-literal consequent nests the same way: the alternate
			// colon-quote outside the brackets stays the ternary separator.
			name:   "ternary_array_branch_then_quoted_alternate",
			source: `flag ? [1, 2] :"no"`,
			want: []ast.TokenType{
				ast.TokenIdent, ast.TokenQuestion, ast.TokenLBracket, ast.TokenInt, ast.TokenComma,
				ast.TokenInt, ast.TokenRBracket, ast.TokenColon, ast.TokenString,
			},
		},
		{
			// A nested ternary inside the consequent hash value completes its own
			// separator at the deeper nesting level, leaving the outer ternary
			// separator to pair the abutting colon-quote alternate.
			name:   "ternary_nested_in_hash_branch_then_quoted_alternate",
			source: `flag ? {a: (g ? 1 : 2)} :"no"`,
			want: []ast.TokenType{
				ast.TokenIdent, ast.TokenQuestion, ast.TokenLBrace, ast.TokenIdent, ast.TokenColon,
				ast.TokenLParen, ast.TokenIdent, ast.TokenQuestion, ast.TokenInt, ast.TokenColon,
				ast.TokenInt, ast.TokenRParen, ast.TokenRBrace, ast.TokenColon, ast.TokenString,
			},
		},
		{
			// Plain nested ternaries: each separator pairs the nearest open `?`
			// at the same nesting level, and an abutting quoted alternate after
			// the last separator stays a separator + string.
			name:   "nested_ternary_abutting_quoted_alternate",
			source: `flag ? a : b ? c :"d"`,
			want: []ast.TokenType{
				ast.TokenIdent, ast.TokenQuestion, ast.TokenIdent, ast.TokenColon, ast.TokenIdent,
				ast.TokenQuestion, ast.TokenIdent, ast.TokenColon, ast.TokenString,
			},
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

	t.Run("keyword_label_string_value", func(t *testing.T) {
		t.Parallel()
		want := []ast.Statement{
			&ast.ExprStmt{Expr: &ast.HashLiteral{Pairs: []ast.HashPair{{
				Key:   &ast.SymbolLiteral{Name: "rescue"},
				Value: &ast.StringLiteral{Value: "x"},
			}}}},
		}
		got, errs := parseSource(t, "def run\n  {rescue:\"x\"}\nend")
		if len(errs) != 0 {
			t.Fatalf("parseSource errors = %v, want none", errs)
		}
		if diff := cmp.Diff(want, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
			t.Fatalf("function body mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("keyword_argument_keyword_name_string_value", func(t *testing.T) {
		t.Parallel()
		got, errs := parseSource(t, "def run\n  greet(begin:\"x\")\nend")
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
		if call.KwArgs[0].Name != "begin" {
			t.Fatalf("keyword name = %q, want %q", call.KwArgs[0].Name, "begin")
		}
		str, ok := call.KwArgs[0].Value.(*ast.StringLiteral)
		if !ok || str.Value != "x" {
			t.Fatalf("keyword value = %#v, want StringLiteral(x)", call.KwArgs[0].Value)
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

	// A hash-literal consequent puts a label colon one nesting level deeper than
	// the ternary `?`. The abutting colon-quote after the closing brace must stay
	// the ternary separator + string alternate, with the consequent the hash,
	// rather than the inner label colon being mistaken for the separator and the
	// alternate misread as a quoted symbol.
	hashBranchCases := []struct {
		name      string
		source    string
		wantPairs int
	}{
		{name: "ternary_hash_branch_single_label", source: "def run\n  flag ? {a: 1} :\"no\"\nend", wantPairs: 1},
		{name: "ternary_hash_branch_multiple_labels", source: "def run\n  flag ? {a: 1, b: 2} :\"no\"\nend", wantPairs: 2},
	}
	for _, tc := range hashBranchCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, errs := parseSource(t, tc.source)
			if len(errs) != 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}
			body := parsedFunctionBody(t, got)
			cond, ok := body[0].(*ast.ExprStmt).Expr.(*ast.ConditionalExpr)
			if !ok {
				t.Fatalf("top expression = %T, want *ast.ConditionalExpr", body[0].(*ast.ExprStmt).Expr)
			}
			hash, ok := cond.Consequent.(*ast.HashLiteral)
			if !ok {
				t.Fatalf("ternary consequent = %#v, want *ast.HashLiteral", cond.Consequent)
			}
			if len(hash.Pairs) != tc.wantPairs {
				t.Fatalf("ternary consequent hash pairs = %d, want %d", len(hash.Pairs), tc.wantPairs)
			}
			str, ok := cond.Alternate.(*ast.StringLiteral)
			if !ok || str.Value != "no" {
				t.Fatalf("ternary alternate = %#v, want StringLiteral(no)", cond.Alternate)
			}
		})
	}
}

// TestParserParenlessCallQuotedSymbolArgument verifies that a quoted symbol
// passed to a parenless call parses as a single positional symbol argument. The
// colon follows an identifier that can end an expression, so an
// expression-end-only test would misread it as a label separator; the space
// before the colon is what distinguishes the argument symbol from a label.
func TestParserParenlessCallQuotedSymbolArgument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   ast.Expression
	}{
		{
			name:   "bare_receiver",
			source: "def run\n  emit :\"foo-bar\"\nend",
			want: &ast.CallExpr{
				Callee: &ast.Identifier{Name: "emit"},
				Args:   []ast.Expression{&ast.SymbolLiteral{Name: "foo-bar"}},
				KwArgs: []ast.KeywordArg{},
			},
		},
		{
			name:   "method_receiver",
			source: "def run\n  obj.emit :\"foo-bar\"\nend",
			want: &ast.CallExpr{
				Callee: &ast.MemberExpr{
					Object:   &ast.Identifier{Name: "obj"},
					Property: "emit",
				},
				Args:   []ast.Expression{&ast.SymbolLiteral{Name: "foo-bar"}},
				KwArgs: []ast.KeywordArg{},
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
			want := []ast.Statement{&ast.ExprStmt{Expr: tc.want}}
			if diff := cmp.Diff(want, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestParserLineLeadingTernaryStringAlternate verifies that a multiline ternary
// whose alternate begins a line with a quoted string and no space after the
// colon keeps the colon as the ternary separator, rather than restarting the
// line as a quoted symbol literal.
func TestParserLineLeadingTernaryStringAlternate(t *testing.T) {
	t.Parallel()

	source := "def run\n  flag ?\n    1\n  :\"no\"\nend"
	got, errs := parseSource(t, source)
	if len(errs) != 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	body := parsedFunctionBody(t, got)
	cond, ok := body[0].(*ast.ExprStmt).Expr.(*ast.ConditionalExpr)
	if !ok {
		t.Fatalf("top expression = %T, want *ast.ConditionalExpr", body[0].(*ast.ExprStmt).Expr)
	}
	if _, ok := cond.Consequent.(*ast.IntegerLiteral); !ok {
		t.Fatalf("ternary consequent = %#v, want IntegerLiteral", cond.Consequent)
	}
	str, ok := cond.Alternate.(*ast.StringLiteral)
	if !ok || str.Value != "no" {
		t.Fatalf("ternary alternate = %#v, want StringLiteral(no)", cond.Alternate)
	}
}

func TestParserPercentArrayReprimeClearsTernaryStateBeforeQuotedSymbol(t *testing.T) {
	t.Parallel()

	source := "def run\n  emit %w?foo? ? 1 :\"no\"\n  :\"sym\"\nend"
	got, errs := parseSource(t, source)
	if len(errs) != 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	body := parsedFunctionBody(t, got)
	if len(body) != 2 {
		t.Fatalf("function body length = %d, want 2", len(body))
	}
	if _, ok := body[0].(*ast.ExprStmt).Expr.(*ast.CallExpr); !ok {
		t.Fatalf("first expression = %T, want *ast.CallExpr", body[0].(*ast.ExprStmt).Expr)
	}
	sym, ok := body[1].(*ast.ExprStmt).Expr.(*ast.SymbolLiteral)
	if !ok || sym.Name != "sym" {
		t.Fatalf("second expression = %#v, want SymbolLiteral(sym)", body[1].(*ast.ExprStmt).Expr)
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
