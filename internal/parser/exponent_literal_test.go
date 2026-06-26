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
// instead of silently lexing the suffix as an identifier. The fraction case
// rides the existing trailing-fraction diagnostic (a well-formed 1e3 followed
// by a stray .5), so it carries a different but equally clear message.
func TestParserMalformedExponentLiterals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		source  string
		wantMsg string
	}{
		{name: "missing exponent digits", source: "1e", wantMsg: "malformed exponent"},
		{name: "sign without digits", source: "1e+", wantMsg: "malformed exponent"},
		{name: "underscore before digits", source: "1e_3", wantMsg: "malformed exponent"},
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
