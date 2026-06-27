package parser

import (
	"math"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

// TestLexerExponentLiterals pins how the lexer tokenizes scientific notation:
// any exponent suffix makes the literal a float, underscores remain visual
// separators in both mantissa and exponent, and the normalized literal drops
// them so downstream parsing can hand it straight to strconv.ParseFloat.
func TestLexerExponentLiterals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		source      string
		wantType    ast.TokenType
		wantLiteral string
	}{
		{name: "integer mantissa", source: "1e3", wantType: ast.TokenFloat, wantLiteral: "1e3"},
		{name: "decimal mantissa", source: "1.5e2", wantType: ast.TokenFloat, wantLiteral: "1.5e2"},
		{name: "uppercase marker", source: "1E6", wantType: ast.TokenFloat, wantLiteral: "1E6"},
		{name: "explicit plus sign", source: "1e+3", wantType: ast.TokenFloat, wantLiteral: "1e+3"},
		{name: "explicit minus sign", source: "1.5e-2", wantType: ast.TokenFloat, wantLiteral: "1.5e-2"},
		{name: "zero exponent", source: "0e0", wantType: ast.TokenFloat, wantLiteral: "0e0"},
		{name: "underscore in exponent", source: "1e1_0", wantType: ast.TokenFloat, wantLiteral: "1e10"},
		{name: "underscore in mantissa", source: "1_0e2", wantType: ast.TokenFloat, wantLiteral: "10e2"},
		{name: "plain integer unaffected", source: "1000", wantType: ast.TokenInt, wantLiteral: "1000"},
		{name: "plain float unaffected", source: "3.14", wantType: ast.TokenFloat, wantLiteral: "3.14"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tok := newLexer(tc.source).NextToken()
			if tok.Type != tc.wantType {
				t.Fatalf("token type = %v, want %v", tok.Type, tc.wantType)
			}
			if tok.Literal != tc.wantLiteral {
				t.Fatalf("token literal = %q, want %q", tok.Literal, tc.wantLiteral)
			}
		})
	}
}

// TestLexerMalformedExponentLiterals pins that a committed exponent suffix
// (one whose marker is followed by a sign or digit) with a missing digit or a
// dangling underscore tokenizes as a single illegal token carrying the
// diagnostic, mirroring Ruby's "trailing `+'/`_' in number" rejection, rather
// than splitting into a float plus a stray identifier.
func TestLexerMalformedExponentLiterals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{name: "trailing underscore", source: "1e3_"},
		{name: "doubled underscore", source: "1e3__4"},
		{name: "sign without digits", source: "1e+"},
		{name: "minus sign without digits", source: "1e-"},
		{name: "sign then underscore", source: "1e+_3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tok := newLexer(tc.source).NextToken()
			if tok.Type != ast.TokenIllegal {
				t.Fatalf("token type = %v, want %v (literal %q)", tok.Type, ast.TokenIllegal, tok.Literal)
			}
			if !strings.Contains(tok.Literal, "malformed exponent") {
				t.Fatalf("token literal = %q, want substring %q", tok.Literal, "malformed exponent")
			}
		})
	}
}

// TestLexerNumberAbuttingIdentifier pins Ruby's rule that a numeric literal
// directly followed by an identifier rune is a malformed numeric literal rather
// than a number split from a trailing identifier. An e/E that is not followed by
// a sign or digit is an ordinary identifier rune, so 1e and 1e_3 fall here too.
// A keyword suffix is exempt because Ruby keeps the keyword (5end, 5if cond).
func TestLexerNumberAbuttingIdentifier(t *testing.T) {
	t.Parallel()
	malformed := []struct {
		name   string
		source string
	}{
		{name: "integer mantissa with identifier", source: "1e3foo"},
		{name: "plain integer with identifier", source: "123foo"},
		{name: "decimal mantissa with identifier", source: "1.5foo"},
		{name: "marker without exponent", source: "1e"},
		{name: "marker before underscore", source: "1e_3"},
		{name: "marker before non-keyword letters", source: "5elf"},
		{name: "trailing underscore identifier", source: "1_foo"},
		{name: "doubled exponent marker", source: "1e3e4"},
	}
	for _, tc := range malformed {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := newLexer(tc.source)
			tok := l.NextToken()
			if tok.Type != ast.TokenIllegal {
				t.Fatalf("token type = %v, want %v (literal %q)", tok.Type, ast.TokenIllegal, tok.Literal)
			}
			if !strings.Contains(tok.Literal, "malformed numeric literal") {
				t.Fatalf("token literal = %q, want substring %q", tok.Literal, "malformed numeric literal")
			}
			// The whole offending run becomes one diagnostic token, leaving
			// nothing for a follow-on identifier.
			if next := l.NextToken(); next.Type != ast.TokenEOF {
				t.Fatalf("token after malformed literal = %v %q, want EOF", next.Type, next.Literal)
			}
		})
	}
}

// TestLexerNumberAbuttingKeyword pins that a numeric literal directly followed
// by a keyword keeps the keyword as its own token, matching Ruby where forms
// like 5if cond and 5end lex as the number followed by the keyword rather than
// a malformed literal. An e/E marker that opens a keyword (5end) must not be
// mistaken for an exponent.
func TestLexerNumberAbuttingKeyword(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		source      string
		wantNumber  ast.TokenType
		wantNumLit  string
		wantKeyword ast.TokenType
	}{
		{name: "integer then end", source: "5end", wantNumber: ast.TokenInt, wantNumLit: "5", wantKeyword: ast.TokenEnd},
		{name: "integer then if", source: "5if", wantNumber: ast.TokenInt, wantNumLit: "5", wantKeyword: ast.TokenIf},
		{name: "float then if", source: "1e3if", wantNumber: ast.TokenFloat, wantNumLit: "1e3", wantKeyword: ast.TokenIf},
		{name: "integer then true", source: "5true", wantNumber: ast.TokenInt, wantNumLit: "5", wantKeyword: ast.TokenTrue},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := newLexer(tc.source)
			number := l.NextToken()
			if number.Type != tc.wantNumber || number.Literal != tc.wantNumLit {
				t.Fatalf("number token = %v %q, want %v %q", number.Type, number.Literal, tc.wantNumber, tc.wantNumLit)
			}
			keyword := l.NextToken()
			if keyword.Type != tc.wantKeyword {
				t.Fatalf("keyword token = %v %q, want %v", keyword.Type, keyword.Literal, tc.wantKeyword)
			}
		})
	}
}

// TestLexerMalformedExponentConsumesTail pins that a committed-but-malformed
// exponent swallows its entire offending tail, so the lexer never leaves a stray
// identifier behind. Without this, [1e+_3] would emit ILLEGAL then a separate
// "_3" identifier token, fragmenting one malformed literal into two tokens. The
// tail includes trailing letters (1e+foo, 5e+end) so Ruby's single "trailing
// `+'" rejection is preserved instead of fragmenting into a keyword/identifier.
func TestLexerMalformedExponentConsumesTail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		source      string
		wantLiteral string
	}{
		{name: "sign then underscore before digits", source: "1e+_3", wantLiteral: "malformed exponent in numeric literal: expected digits after 'e'"},
		{name: "sign then identifier", source: "1e+foo", wantLiteral: "malformed exponent in numeric literal: expected digits after 'e'"},
		{name: "sign then keyword", source: "5e+end", wantLiteral: "malformed exponent in numeric literal: expected digits after 'e'"},
		{name: "trailing underscore", source: "1e3_", wantLiteral: "malformed exponent in numeric literal: underscore must sit between exponent digits"},
		{name: "doubled underscore", source: "1e3__4", wantLiteral: "malformed exponent in numeric literal: underscore must sit between exponent digits"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := newLexer(tc.source)
			tok := l.NextToken()
			if tok.Type != ast.TokenIllegal {
				t.Fatalf("first token type = %v, want %v (literal %q)", tok.Type, ast.TokenIllegal, tok.Literal)
			}
			if tok.Literal != tc.wantLiteral {
				t.Fatalf("first token literal = %q, want %q", tok.Literal, tc.wantLiteral)
			}
			// The entire malformed source must be consumed by the single
			// diagnostic token, leaving nothing for a follow-on identifier.
			if next := l.NextToken(); next.Type != ast.TokenEOF {
				t.Fatalf("token after malformed exponent = %v %q, want EOF", next.Type, next.Literal)
			}
		})
	}
}

// TestParserMalformedNumericInDelimitedContext pins that a malformed numeric
// literal inside brackets reports exactly one diagnostic rather than cascading
// into spurious "expected ]" and "unexpected ]" errors caused by a leftover
// identifier token. Both the committed-exponent path (1e+_3) and the
// number-abutting-identifier path (1e_3) must consume their entire offending run.
func TestParserMalformedNumericInDelimitedContext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		source  string
		wantMsg string
	}{
		{name: "committed exponent", source: "[1e+_3]", wantMsg: "malformed exponent"},
		{name: "number abutting identifier", source: "[1e_3]", wantMsg: "malformed numeric literal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, tc.source)
			if len(errs) != 1 {
				var sb strings.Builder
				for _, err := range errs {
					sb.WriteString(err.Error())
					sb.WriteByte('\n')
				}
				t.Fatalf("parseSource(%q) produced %d errors, want 1:\n%s", tc.source, len(errs), sb.String())
			}
			if !strings.Contains(errs[0].Error(), tc.wantMsg) {
				t.Fatalf("error = %q, want substring %q", errs[0].Error(), tc.wantMsg)
			}
		})
	}
}

// TestParserIllegalCharacterDiagnostic pins that a raw unsupported character
// reports the generic "unexpected token invalid token" message. The illegal
// token's literal is the offending rune, not a lexer diagnostic, so it must not
// be surfaced verbatim the way malformed-literal diagnostics are.
func TestParserIllegalCharacterDiagnostic(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"$", "`"} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, source)
			if len(errs) == 0 {
				t.Fatalf("parseSource(%q) errors = nil, want a diagnostic", source)
			}
			var sb strings.Builder
			for _, err := range errs {
				sb.WriteString(err.Error())
				sb.WriteByte('\n')
			}
			if !strings.Contains(sb.String(), "unexpected token invalid token") {
				t.Fatalf("parseSource(%q) errors = %s, want substring %q", source, sb.String(), "unexpected token invalid token")
			}
			if strings.Contains(sb.String(), "parse error at 1:1: "+source) {
				t.Fatalf("parseSource(%q) leaked the raw character into the diagnostic: %s", source, sb.String())
			}
		})
	}
}

// TestParserExponentLiteralValues confirms exponent literals parse to floats
// with the value Ruby produces, including overflow that saturates to infinity.
func TestParserExponentLiteralValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   float64
	}{
		{name: "integer mantissa", source: "1e3", want: 1000},
		{name: "decimal mantissa negative exponent", source: "1.5e-2", want: 0.015},
		{name: "uppercase marker", source: "1E6", want: 1000000},
		{name: "explicit plus sign", source: "1e+3", want: 1000},
		{name: "underscore separators", source: "1e1_0", want: 1e10},
		{name: "overflow saturates to infinity", source: "1e1000", want: math.Inf(1)},
		{name: "underflow saturates to zero", source: "1e-400", want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}
			lit := singleFloatLiteral(t, got)
			if lit.Value != tc.want {
				t.Fatalf("FloatLiteral value = %v, want %v", lit.Value, tc.want)
			}
		})
	}
}

// TestParserExponentLiteralAST checks the structural shape: a bare exponent
// literal is a FloatLiteral, and exponent literals compose normally with
// binary operators rather than fragmenting into identifier lookups.
func TestParserExponentLiteralAST(t *testing.T) {
	t.Parallel()

	source := `def run
  2e2 + 1
end`
	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{
			Expr: &ast.BinaryExpr{
				Left:     &ast.FloatLiteral{Value: 200},
				Operator: ast.TokenPlus,
				Right:    &ast.IntegerLiteral{Value: 1},
			},
		},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// TestParserMalformedExponentLiterals pins the issue's hardening requirement:
// partial exponent forms report a clear diagnostic at the offending literal
// instead of silently lexing the suffix as an identifier. Forms whose e/E marker
// is not followed by a sign or digit (1e, 1e_3) are not exponents at all but a
// number abutting an identifier, so they carry the numeric-literal diagnostic.
// The fraction case rides the existing trailing-fraction diagnostic (a
// well-formed 1e3 followed by a stray .5), so it carries a different but equally
// clear message.
func TestParserMalformedExponentLiterals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		source  string
		wantMsg string
	}{
		{name: "missing exponent digits", source: "1e", wantMsg: "malformed numeric literal"},
		{name: "sign without digits", source: "1e+", wantMsg: "malformed exponent"},
		{name: "underscore before digits", source: "1e_3", wantMsg: "malformed numeric literal"},
		{name: "trailing underscore", source: "1e3_", wantMsg: "malformed exponent"},
		{name: "doubled underscore", source: "1e3__4", wantMsg: "malformed exponent"},
		{name: "identifier after exponent", source: "1e3foo", wantMsg: "malformed numeric literal"},
		{name: "fraction after exponent", source: "1e3.5", wantMsg: "expected member name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, tc.source)
			if len(errs) == 0 {
				t.Fatalf("parseSource(%q) errors = nil, want a diagnostic", tc.source)
			}
			var sb strings.Builder
			for _, err := range errs {
				sb.WriteString(err.Error())
				sb.WriteByte('\n')
			}
			if !strings.Contains(sb.String(), tc.wantMsg) {
				t.Fatalf("parseSource(%q) errors = %s, want substring %q", tc.source, sb.String(), tc.wantMsg)
			}
		})
	}
}

// TestParserExponentMarkerDoesNotShadowMethodCalls guards the disambiguation
// boundary: a trailing-dot method call whose name starts with e/E must stay a
// method call, never an exponent suffix.
func TestParserExponentMarkerDoesNotShadowMethodCalls(t *testing.T) {
	t.Parallel()

	source := "1.e3"
	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}
	if len(got.Statements) != 1 {
		t.Fatalf("parseSource(%q) statements = %d, want 1", source, len(got.Statements))
	}
	stmt, ok := got.Statements[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("statement = %T, want *ast.ExprStmt", got.Statements[0])
	}
	member, ok := stmt.Expr.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expr = %T, want *ast.MemberExpr (method call, not exponent)", stmt.Expr)
	}
	if member.Property != "e3" {
		t.Fatalf("member property = %q, want %q", member.Property, "e3")
	}
	if _, ok := member.Object.(*ast.IntegerLiteral); !ok {
		t.Fatalf("member object = %T, want *ast.IntegerLiteral", member.Object)
	}
}

// TestParserNumberAbuttingModifierKeyword pins that a numeric literal directly
// followed by a modifier keyword parses as the modifier statement, matching Ruby
// where 5if cond and 1e3if cond are valid. The numeric-suffix guard must exempt
// keyword suffixes so these forms are not rejected as malformed literals.
func TestParserNumberAbuttingModifierKeyword(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		source    string
		wantValue any
	}{
		{name: "integer if modifier", source: "5if true", wantValue: int64(5)},
		{name: "float if modifier", source: "1e3if true", wantValue: float64(1000)},
		{name: "integer while modifier", source: "5while false", wantValue: int64(5)},
		{name: "integer unless modifier", source: "5unless false", wantValue: int64(5)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}
			if len(got.Statements) != 1 {
				t.Fatalf("parseSource(%q) statements = %d, want 1", tc.source, len(got.Statements))
			}
			var body []ast.Statement
			switch stmt := got.Statements[0].(type) {
			case *ast.IfStmt:
				body = stmt.Consequent
				if len(body) == 0 {
					body = stmt.Alternate
				}
			case *ast.WhileStmt:
				body = stmt.Body
			default:
				t.Fatalf("statement = %T, want a modifier statement", got.Statements[0])
			}
			if len(body) != 1 {
				t.Fatalf("modifier body = %d statements, want 1", len(body))
			}
			expr, ok := body[0].(*ast.ExprStmt)
			if !ok {
				t.Fatalf("modifier body = %T, want *ast.ExprStmt", body[0])
			}
			switch want := tc.wantValue.(type) {
			case int64:
				lit, ok := expr.Expr.(*ast.IntegerLiteral)
				if !ok || lit.Value != want {
					t.Fatalf("modifier body expr = %#v, want IntegerLiteral %d", expr.Expr, want)
				}
			case float64:
				lit, ok := expr.Expr.(*ast.FloatLiteral)
				if !ok || lit.Value != want {
					t.Fatalf("modifier body expr = %#v, want FloatLiteral %v", expr.Expr, want)
				}
			}
		})
	}
}

func singleFloatLiteral(t testing.TB, program *ast.Program) *ast.FloatLiteral {
	t.Helper()
	if len(program.Statements) != 1 {
		t.Fatalf("program has %d statements, want 1", len(program.Statements))
	}
	stmt, ok := program.Statements[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("statement = %T, want *ast.ExprStmt", program.Statements[0])
	}
	lit, ok := stmt.Expr.(*ast.FloatLiteral)
	if !ok {
		t.Fatalf("expr = %T, want *ast.FloatLiteral", stmt.Expr)
	}
	return lit
}
