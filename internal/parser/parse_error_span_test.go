package parser

import (
	"errors"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

type positionedError interface {
	error
	Pos() ast.Position
	End() ast.Position
	Message() string
}

func TestParseErrorsExposeStructuredPositions(t *testing.T) {
	t.Parallel()
	_, errs := Parse("def 123()\n  1\nend\n")
	if len(errs) == 0 {
		t.Fatal("expected parse errors for invalid function name")
	}

	var first positionedError
	if !errors.As(errs[0], &first) {
		t.Fatalf("errs[0] = %T, want positioned parse error", errs[0])
	}
	if got, want := first.Pos(), (ast.Position{Line: 1, Column: 5}); got != want {
		t.Fatalf("Pos() = %v, want %v", got, want)
	}
	// The offending token is the three-character literal "123", so the
	// exclusive end lands three columns after the start.
	if got, want := first.End(), (ast.Position{Line: 1, Column: 8}); got != want {
		t.Fatalf("End() = %v, want %v", got, want)
	}
	if got := first.Message(); got != "expected function name, got integer" {
		t.Fatalf("Message() = %q, want bare message", got)
	}
	if strings.Contains(first.Message(), "parse error at") {
		t.Fatalf("Message() %q must not embed the position prefix", first.Message())
	}
	if !strings.Contains(first.Error(), "parse error at 1:5") {
		t.Fatalf("Error() = %q, want position-prefixed rendering", first.Error())
	}
}

func TestParseErrorEndIsZeroAtEndOfInput(t *testing.T) {
	t.Parallel()
	_, errs := Parse("def run()\n  x = [1,\nend\n")
	if len(errs) == 0 {
		t.Fatal("expected parse errors for unterminated array literal")
	}

	var sawUnknownSpan bool
	for _, err := range errs {
		var pe positionedError
		if !errors.As(err, &pe) {
			t.Fatalf("error %T does not expose positions", err)
		}
		if pe.End() == (ast.Position{}) {
			sawUnknownSpan = true
			if pe.Pos() == (ast.Position{}) {
				t.Fatalf("error %q has neither position nor span", pe.Message())
			}
		}
	}
	if !sawUnknownSpan {
		t.Fatal("expected at least one end-of-input error with an unknown span")
	}
}

// TestLexerStampsSourceAccurateTokenEnds pins that token spans come from
// the source text, not the normalized literal: strings lose their quotes
// and escapes, symbols and ivars drop their sigils, and numeric literals
// drop underscores, so literal length must not drive the span.
func TestLexerStampsSourceAccurateTokenEnds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		source  string
		tokType ast.TokenType
		wantPos ast.Position
		wantEnd ast.Position
	}{
		{
			name:    "identifier",
			source:  "total",
			tokType: ast.TokenIdent,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 6},
		},
		{
			name:    "string_span_includes_quotes",
			source:  `"abc"`,
			tokType: ast.TokenString,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 6},
		},
		{
			name:    "string_span_includes_escapes",
			source:  `"a\"b"`,
			tokType: ast.TokenString,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 7},
		},
		{
			name:    "single_quoted_string_span_includes_quotes",
			source:  `'abc'`,
			tokType: ast.TokenString,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 6},
		},
		{
			name:    "single_quoted_string_span_includes_escapes",
			source:  `'a\'b'`,
			tokType: ast.TokenString,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 7},
		},
		{
			name:    "integer_span_includes_underscores",
			source:  "1_000",
			tokType: ast.TokenInt,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 6},
		},
		{
			name:    "symbol_span_includes_sigil",
			source:  ":name",
			tokType: ast.TokenSymbol,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 6},
		},
		{
			name:    "percent_word_array_span_includes_delimiters",
			source:  "%w[alpha beta]",
			tokType: ast.TokenWords,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 15},
		},
		{
			name:    "percent_symbol_array_span_includes_delimiters",
			source:  "%i[alpha beta]",
			tokType: ast.TokenSymbols,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 15},
		},
		{
			name:    "percent_interpolated_word_array_span_includes_delimiters",
			source:  "%W[alpha beta]",
			tokType: ast.TokenInterpWords,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 15},
		},
		{
			name:    "percent_interpolated_symbol_array_span_includes_delimiters",
			source:  "%I[alpha beta]",
			tokType: ast.TokenInterpSymbols,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 15},
		},
		{
			name:    "ivar_span_includes_sigil",
			source:  "@count",
			tokType: ast.TokenIvar,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 7},
		},
		{
			name:    "spaceship_span_includes_all_runes",
			source:  "<=>",
			tokType: ast.TokenSpaceship,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 4},
		},
		{
			name:    "case_equality_span_includes_all_runes",
			source:  "===",
			tokType: ast.TokenCaseEQ,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 4},
		},
		{
			name:    "multibyte_runes_counted_once",
			source:  `"héllo"`,
			tokType: ast.TokenString,
			wantPos: ast.Position{Line: 1, Column: 1},
			wantEnd: ast.Position{Line: 1, Column: 8},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tok := newLexer(tc.source).NextToken()
			if tok.Type != tc.tokType {
				t.Fatalf("token type = %v, want %v", tok.Type, tc.tokType)
			}
			if tok.Pos != tc.wantPos {
				t.Fatalf("Pos = %v, want %v", tok.Pos, tc.wantPos)
			}
			if tok.End != tc.wantEnd {
				t.Fatalf("End = %v, want %v", tok.End, tc.wantEnd)
			}
		})
	}
}

func TestLexerSymbolSigilSupportsIdentifiersAndOperators(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		source      string
		wantFirst   ast.TokenType
		wantSecond  ast.TokenType
		wantLiteral string
	}{
		{
			name:        "identifier symbol lexes whole",
			source:      ":concat",
			wantFirst:   ast.TokenSymbol,
			wantSecond:  ast.TokenEOF,
			wantLiteral: "concat",
		},
		{
			name:        "plus operator lexes as symbol",
			source:      ":+",
			wantFirst:   ast.TokenSymbol,
			wantSecond:  ast.TokenEOF,
			wantLiteral: "+",
		},
		{
			name:        "power operator lexes as symbol",
			source:      ":**",
			wantFirst:   ast.TokenSymbol,
			wantSecond:  ast.TokenEOF,
			wantLiteral: "**",
		},
		{
			name:        "index operator lexes as symbol",
			source:      ":[]",
			wantFirst:   ast.TokenSymbol,
			wantSecond:  ast.TokenEOF,
			wantLiteral: "[]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lex := newLexer(tc.source)
			got := lex.NextToken()
			if got.Type != tc.wantFirst {
				t.Fatalf("NextToken(%q) type = %v, want %v", tc.source, got.Type, tc.wantFirst)
			}
			if got.Literal != tc.wantLiteral {
				t.Fatalf("NextToken(%q) literal = %q, want %q", tc.source, got.Literal, tc.wantLiteral)
			}
			if got := lex.NextToken(); got.Type != tc.wantSecond {
				t.Fatalf("second token after %q = %v, want %v", tc.source, got.Type, tc.wantSecond)
			}
		})
	}
}

func TestLexerTokenEndAtEOFIsZero(t *testing.T) {
	t.Parallel()
	lex := newLexer("x")
	if tok := lex.NextToken(); tok.Type != ast.TokenIdent {
		t.Fatalf("first token = %v, want identifier", tok.Type)
	}
	eof := lex.NextToken()
	if eof.Type != ast.TokenEOF {
		t.Fatalf("second token = %v, want EOF", eof.Type)
	}
	if eof.End != (ast.Position{}) {
		t.Fatalf("EOF End = %v, want zero", eof.End)
	}
}

// TestParseErrorSpanCoversFullStringToken is the review example: the
// offending string token in `def "abc"()` must span its quotes.
func TestParseErrorSpanCoversFullStringToken(t *testing.T) {
	t.Parallel()
	_, errs := Parse("def \"abc\"()\n  1\nend\n")
	if len(errs) == 0 {
		t.Fatal("expected parse errors for string function name")
	}
	var first positionedError
	if !errors.As(errs[0], &first) {
		t.Fatalf("errs[0] = %T, want positioned parse error", errs[0])
	}
	if got, want := first.Pos(), (ast.Position{Line: 1, Column: 5}); got != want {
		t.Fatalf("Pos() = %v, want %v", got, want)
	}
	if got, want := first.End(), (ast.Position{Line: 1, Column: 10}); got != want {
		t.Fatalf("End() = %v, want %v (span must include both quotes)", got, want)
	}
}
