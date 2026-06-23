package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserKeywordArgumentShorthand(t *testing.T) {
	t.Parallel()

	source := `def run
  takes(name:, age: 42)
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.Identifier{Name: "takes"},
			Args:   []ast.Expression{},
			KwArgs: []ast.KeywordArg{
				{Name: "name", Value: &ast.Identifier{Name: "name"}},
				{Name: "age", Value: &ast.IntegerLiteral{Value: 42}},
			},
			KeywordOptionsHash: true,
		}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// TestParserParenlessReservedKeywordLabels verifies that reserved keywords
// that are not prefix expressions (such as `rescue`, `ensure`, and `begin`)
// can still start a parenless keyword-argument call. These tokens are valid
// only as labels here, so the trailing colon disambiguates them from their
// keyword meaning, mirroring Ruby's parenless keyword-argument syntax.
func TestParserParenlessReservedKeywordLabels(t *testing.T) {
	t.Parallel()

	source := `def run
  record rescue: "retry"
  configure ok: 1, rescue: 2, ensure: 3
  begin_with begin: 1
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.Identifier{Name: "record"},
			Args:   []ast.Expression{},
			KwArgs: []ast.KeywordArg{
				{Name: "rescue", Value: &ast.StringLiteral{Value: "retry"}},
			},
			KeywordOptionsHash: true,
		}},
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.Identifier{Name: "configure"},
			Args:   []ast.Expression{},
			KwArgs: []ast.KeywordArg{
				{Name: "ok", Value: &ast.IntegerLiteral{Value: 1}},
				{Name: "rescue", Value: &ast.IntegerLiteral{Value: 2}},
				{Name: "ensure", Value: &ast.IntegerLiteral{Value: 3}},
			},
			KeywordOptionsHash: true,
		}},
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.Identifier{Name: "begin_with"},
			Args:   []ast.Expression{},
			KwArgs: []ast.KeywordArg{
				{Name: "begin", Value: &ast.IntegerLiteral{Value: 1}},
			},
			KeywordOptionsHash: true,
		}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// TestParserParenlessReservedKeywordWithoutColonNotCall verifies that a bare
// reserved keyword following a callee is not misread as a parenless call. The
// colon is required to treat the keyword as a label, so `record rescue 2`
// keeps `rescue` as a keyword and reports a parse error rather than silently
// parsing a call.
func TestParserParenlessReservedKeywordWithoutColonNotCall(t *testing.T) {
	t.Parallel()

	source := `def run
  record a, rescue 2
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = nil, want diagnostic for bare reserved keyword", source)
	}
}

func TestParserWordBooleanKeywordArguments(t *testing.T) {
	t.Parallel()

	source := `def run
  takes(and: 1, or: 2, not: 3)
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.Identifier{Name: "takes"},
			Args:   []ast.Expression{},
			KwArgs: []ast.KeywordArg{
				{Name: "and", Value: &ast.IntegerLiteral{Value: 1}},
				{Name: "or", Value: &ast.IntegerLiteral{Value: 2}},
				{Name: "not", Value: &ast.IntegerLiteral{Value: 3}},
			},
			KeywordOptionsHash: true,
		}},
	}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// TestParserParenthesizedKeywordOptionsHashScope verifies that parenthesized
// calls with keyword arguments mark KeywordOptionsHash for every callee shape
// and record the parenthesized call form. The parser cannot tell a function
// value's `call` alias from a method named `call`, so it defers the
// collapse-vs-strict decision to the runtime, which keeps parenthesized method
// and constructor calls strict (issue #576) while preserving direct-call parity
// for `fn.call(...)` (issue #589).
func TestParserParenthesizedKeywordOptionsHashScope(t *testing.T) {
	t.Parallel()

	source := `def run
  configure(retries: 3)
  obj.configure(retries: 3)
  Server.new(retries: 3)
  handler.call(retries: 3)
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.Identifier{Name: "configure"},
			Args:   []ast.Expression{},
			KwArgs: []ast.KeywordArg{
				{Name: "retries", Value: &ast.IntegerLiteral{Value: 3}},
			},
			KeywordOptionsHash: true,
			Parenthesized:      true,
		}},
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.MemberExpr{
				Object:   &ast.Identifier{Name: "obj"},
				Property: "configure",
			},
			Args: []ast.Expression{},
			KwArgs: []ast.KeywordArg{
				{Name: "retries", Value: &ast.IntegerLiteral{Value: 3}},
			},
			KeywordOptionsHash: true,
			Parenthesized:      true,
		}},
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.MemberExpr{
				Object:   &ast.Identifier{Name: "Server"},
				Property: "new",
			},
			Args: []ast.Expression{},
			KwArgs: []ast.KeywordArg{
				{Name: "retries", Value: &ast.IntegerLiteral{Value: 3}},
			},
			KeywordOptionsHash: true,
			Parenthesized:      true,
		}},
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.MemberExpr{
				Object:   &ast.Identifier{Name: "handler"},
				Property: "call",
			},
			Args: []ast.Expression{},
			KwArgs: []ast.KeywordArg{
				{Name: "retries", Value: &ast.IntegerLiteral{Value: 3}},
			},
			KeywordOptionsHash: true,
			Parenthesized:      true,
		}},
	}
	// astCmpOpts ignores Parenthesized, so compare with an options set that only
	// drops source positions to lock in the parenthesized call form alongside
	// the structural shape.
	strictCmpOpts := cmp.Options{cmpopts.IgnoreFields(ast.Position{}, "Line", "Column")}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), strictCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

// TestParserParenlessKeywordOptionsHashForm verifies that a parenless keyword
// call records the parenless form so the runtime collapses an options hash for
// any options-hash target, including method calls.
func TestParserParenlessKeywordOptionsHashForm(t *testing.T) {
	t.Parallel()

	source := `def run
  configure retries: 3
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Callee: &ast.Identifier{Name: "configure"},
			Args:   []ast.Expression{},
			KwArgs: []ast.KeywordArg{
				{Name: "retries", Value: &ast.IntegerLiteral{Value: 3}},
			},
			KeywordOptionsHash: true,
			Parenthesized:      false,
		}},
	}
	strictCmpOpts := cmp.Options{cmpopts.IgnoreFields(ast.Position{}, "Line", "Column")}
	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), strictCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}
