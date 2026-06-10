package parser

import (
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserTrailingBraceBlockContinuesChainedCall(t *testing.T) {
	t.Parallel()

	source := `def run(players)
  players.map { |p| p[:status] }.tally
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	exprStmt, ok := body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("parseSource(%q) body[0] = %T, want *ast.ExprStmt", source, body[0])
	}
	tally, ok := exprStmt.Expr.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("parseSource(%q) expression = %T, want *ast.MemberExpr", source, exprStmt.Expr)
	}
	if tally.Property != "tally" {
		t.Fatalf("parseSource(%q) chained property = %q, want %q", source, tally.Property, "tally")
	}

	mapCall, ok := tally.Object.(*ast.CallExpr)
	if !ok {
		t.Fatalf("parseSource(%q) tally receiver = %T, want *ast.CallExpr", source, tally.Object)
	}
	if mapCall.Block == nil {
		t.Fatalf("parseSource(%q) map block = nil, want brace block", source)
	}
	if len(mapCall.Block.Params) != 1 || mapCall.Block.Params[0].Name != "p" {
		t.Fatalf("parseSource(%q) block params = %#v, want p", source, mapCall.Block.Params)
	}
	if len(mapCall.Block.Body) != 1 {
		t.Fatalf("parseSource(%q) block body length = %d, want 1", source, len(mapCall.Block.Body))
	}

	mapMember, ok := mapCall.Callee.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("parseSource(%q) map callee = %T, want *ast.MemberExpr", source, mapCall.Callee)
	}
	if mapMember.Property != "map" {
		t.Fatalf("parseSource(%q) map property = %q, want %q", source, mapMember.Property, "map")
	}
}

func TestParserLineInitialBraceStillStartsHashLiteral(t *testing.T) {
	t.Parallel()

	source := `def run
  value
  { ok: true }
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	body := parsedFunctionBody(t, got)
	if len(body) != 2 {
		t.Fatalf("parseSource(%q) body length = %d, want 2", source, len(body))
	}

	first, ok := body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("parseSource(%q) body[0] = %T, want *ast.ExprStmt", source, body[0])
	}
	if _, ok := first.Expr.(*ast.Identifier); !ok {
		t.Fatalf("parseSource(%q) first expression = %T, want *ast.Identifier", source, first.Expr)
	}

	second, ok := body[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("parseSource(%q) body[1] = %T, want *ast.ExprStmt", source, body[1])
	}
	if _, ok := second.Expr.(*ast.HashLiteral); !ok {
		t.Fatalf("parseSource(%q) second expression = %T, want *ast.HashLiteral", source, second.Expr)
	}
}
