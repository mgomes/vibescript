package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserNumericBasePrefixIntegers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   int64
	}{
		{name: "hex lowercase", source: "0x10", want: 16},
		{name: "hex uppercase prefix", source: "0X10", want: 16},
		{name: "hex letters", source: "0xAbC", want: 2748},
		{name: "hex max byte", source: "0xFF", want: 255},
		{name: "binary lowercase", source: "0b1010", want: 10},
		{name: "binary uppercase prefix", source: "0B1010", want: 10},
		{name: "octal lowercase", source: "0o12", want: 10},
		{name: "octal uppercase prefix", source: "0O12", want: 10},
		{name: "decimal lowercase", source: "0d12", want: 12},
		{name: "decimal uppercase prefix", source: "0D12", want: 12},
		{name: "hex underscores", source: "0xff_ff", want: 65535},
		{name: "binary underscores", source: "0b1_0_1", want: 5},
		{name: "hex deadbeef underscores", source: "0xDEAD_BEEF", want: 3735928559},
		{name: "decimal underscores", source: "0d1_000", want: 1000},
		// A leading zero without a base marker stays decimal rather than
		// being read as a legacy octal literal.
		{name: "leading zero is decimal", source: "010", want: 10},
		{name: "bare zero", source: "0", want: 0},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source := "def run\n  " + tc.source + "\nend"
			got, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}

			wantBody := []ast.Statement{
				&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: tc.want}},
			}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLexerNumericBasePrefixTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		literal string
	}{
		{name: "hex strips underscores", source: "0xff_ff", literal: "0xffff"},
		{name: "binary strips underscores", source: "0b1_0_1", literal: "0b101"},
		{name: "octal", source: "0o12", literal: "0o12"},
		{name: "decimal prefix", source: "0d12", literal: "0d12"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tok := newLexer(tc.source).NextToken()
			if tok.Type != ast.TokenInt {
				t.Fatalf("NextToken(%q) type = %s, want %s", tc.source, tok.Type, ast.TokenInt)
			}
			if tok.Literal != tc.literal {
				t.Fatalf("NextToken(%q) literal = %q, want %q", tc.source, tok.Literal, tc.literal)
			}
		})
	}
}

func TestLexerNumericBasePrefixOperatorSuffix(t *testing.T) {
	t.Parallel()

	// The '?' and '!' runes are operators that terminate a based literal rather
	// than gluing onto it; the literal must lex as an integer followed by the
	// operator token, exactly as the decimal path lexes "1?" and "1!".
	tests := []struct {
		name     string
		source   string
		literal  string
		nextType ast.TokenType
	}{
		{name: "hex then ternary", source: "0x1?", literal: "0x1", nextType: ast.TokenQuestion},
		{name: "binary then ternary", source: "0b1?", literal: "0b1", nextType: ast.TokenQuestion},
		{name: "octal then ternary", source: "0o7?", literal: "0o7", nextType: ast.TokenQuestion},
		{name: "decimal prefix then ternary", source: "0d9?", literal: "0d9", nextType: ast.TokenQuestion},
		{name: "hex then bang", source: "0x1!", literal: "0x1", nextType: ast.TokenBang},
		{name: "binary then bang", source: "0b1!", literal: "0b1", nextType: ast.TokenBang},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := newLexer(tc.source)
			num := l.NextToken()
			if num.Type != ast.TokenInt || num.Literal != tc.literal {
				t.Fatalf("NextToken(%q) = (%s, %q), want (%s, %q)", tc.source, num.Type, num.Literal, ast.TokenInt, tc.literal)
			}
			op := l.NextToken()
			if op.Type != tc.nextType {
				t.Fatalf("second token of %q = %s, want %s", tc.source, op.Type, tc.nextType)
			}
		})
	}
}

func TestParserNumericBasePrefixTernary(t *testing.T) {
	t.Parallel()

	// A based literal as a ternary condition must parse as a ConditionalExpr,
	// identical to the equivalent decimal literal; the '?' must not be folded
	// into the number as an invalid trailing rune.
	tests := []struct {
		name      string
		source    string
		condition ast.Expression
	}{
		{name: "hex condition", source: "0x1 ? 2 : 3", condition: &ast.IntegerLiteral{Value: 1}},
		{name: "binary condition", source: "0b1 ? 2 : 3", condition: &ast.IntegerLiteral{Value: 1}},
		{name: "octal condition", source: "0o5 ? 2 : 3", condition: &ast.IntegerLiteral{Value: 5}},
		{name: "decimal prefix condition", source: "0d9 ? 2 : 3", condition: &ast.IntegerLiteral{Value: 9}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source := "def run\n  " + tc.source + "\nend"
			got, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}

			wantBody := []ast.Statement{
				&ast.ExprStmt{Expr: &ast.ConditionalExpr{
					Condition:  tc.condition,
					Consequent: &ast.IntegerLiteral{Value: 2},
					Alternate:  &ast.IntegerLiteral{Value: 3},
				}},
			}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParserNumericBasePrefixErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{name: "hex without digits", source: "0x"},
		{name: "binary without digits", source: "0b"},
		{name: "octal without digits", source: "0o"},
		{name: "decimal without digits", source: "0d"},
		{name: "hex leading underscore", source: "0x_1"},
		{name: "hex trailing underscore", source: "0x1_"},
		{name: "binary out of range first digit", source: "0b2"},
		{name: "octal out of range digit", source: "0o8"},
		{name: "hex stray letter", source: "0xg"},
		{name: "hex stray letter after digits", source: "0x1g"},
		{name: "binary fractional part", source: "0b1010.5"},
		{name: "decimal fractional part", source: "0d12.5"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source := "def run\n  " + tc.source + "\nend"
			_, errs := parseSource(t, source)
			if len(errs) == 0 {
				t.Fatalf("parseSource(%q) errors = nil, want an invalid numeric literal error", tc.source)
			}

			var got strings.Builder
			for _, err := range errs {
				got.WriteString(err.Error())
				got.WriteByte('\n')
			}
			if !strings.Contains(got.String(), invalidNumericLiteral) {
				t.Fatalf("parseSource(%q) errors = %s, want substring %q", tc.source, got.String(), invalidNumericLiteral)
			}
		})
	}
}

func TestLexerNumericBasePrefixIllegalToken(t *testing.T) {
	t.Parallel()

	tok := newLexer("0xg").NextToken()
	if tok.Type != ast.TokenIllegal || tok.Literal != invalidNumericLiteral {
		t.Fatalf("NextToken(%q) = (%s, %q), want (%s, %q)", "0xg", tok.Type, tok.Literal, ast.TokenIllegal, invalidNumericLiteral)
	}
}

func TestLexerInvalidNumericLiteralResumesScanning(t *testing.T) {
	t.Parallel()

	// After a malformed based literal the lexer must resume on the next
	// real token rather than re-lexing the literal's tail as an identifier.
	l := newLexer("0xg + 1")
	illegal := l.NextToken()
	if illegal.Type != ast.TokenIllegal || illegal.Literal != invalidNumericLiteral {
		t.Fatalf("first token = (%s, %q), want (%s, %q)", illegal.Type, illegal.Literal, ast.TokenIllegal, invalidNumericLiteral)
	}
	plus := l.NextToken()
	if plus.Type != ast.TokenPlus {
		t.Fatalf("second token = %s, want %s", plus.Type, ast.TokenPlus)
	}
	one := l.NextToken()
	if one.Type != ast.TokenInt || one.Literal != "1" {
		t.Fatalf("third token = (%s, %q), want (%s, %q)", one.Type, one.Literal, ast.TokenInt, "1")
	}
}
