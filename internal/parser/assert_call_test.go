package parser

import (
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

// assert was parsed as a bespoke statement form, so `assert(cond, "msg")`
// read the parentheses as a grouped expression and tripped on the comma --
// even though its documented signature takes two arguments and every other
// multi-argument builtin accepts the parenthesised form.
func TestAssertParsesParenthesisedCall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		wantArgs int
		wantKw   bool
	}{
		{name: "condition and message", source: `assert(true, "msg")`, wantArgs: 2},
		{name: "condition only", source: `assert(true)`, wantArgs: 1},
		{name: "keyword message", source: `assert(true, message: "msg")`, wantArgs: 1, wantKw: true},
		{name: "expression arguments", source: `assert(a > b, "msg")`, wantArgs: 2},
		{name: "trailing call argument", source: `assert(list.empty?, "msg")`, wantArgs: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			program, errs := parseSource(t, tc.source)
			if len(errs) != 0 {
				t.Fatalf("%s: unexpected parse errors: %v", tc.source, errs)
			}
			call := soleAssertCall(t, program)
			if got := len(call.Args); got != tc.wantArgs {
				t.Fatalf("%s parsed %d positional args, want %d", tc.source, got, tc.wantArgs)
			}
			if gotKw := len(call.KwArgs) > 0; gotKw != tc.wantKw {
				t.Fatalf("%s keyword args present = %v, want %v", tc.source, gotKw, tc.wantKw)
			}
		})
	}
}

// A space before the parentheses makes them a grouped first argument, so the
// paren-less form must survive the change: `assert (a > b), "msg"` is a
// two-argument call, not a one-argument one.
func TestAssertSpacedParenthesisStaysParenless(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		wantArgs int
	}{
		{name: "grouped condition and message", source: `assert (1 > 0), "msg"`, wantArgs: 2},
		{name: "grouped condition alone", source: `assert (1 > 0)`, wantArgs: 1},
		{name: "bare condition and message", source: `assert 1 > 0, "msg"`, wantArgs: 2},
		{name: "bare condition alone", source: `assert 1 > 0`, wantArgs: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			program, errs := parseSource(t, tc.source)
			if len(errs) != 0 {
				t.Fatalf("%s: unexpected parse errors: %v", tc.source, errs)
			}
			call := soleAssertCall(t, program)
			if got := len(call.Args); got != tc.wantArgs {
				t.Fatalf("%s parsed %d args, want %d", tc.source, got, tc.wantArgs)
			}
		})
	}
}

// A parenthesised assert is an ordinary expression statement, so statement
// modifiers still apply to it.
func TestAssertParenthesisedAcceptsStatementModifier(t *testing.T) {
	t.Parallel()
	program, errs := parseSource(t, `assert(true, "msg") if ready`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if len(program.Statements) != 1 {
		t.Fatalf("parsed %d statements, want 1", len(program.Statements))
	}
	if _, ok := program.Statements[0].(*ast.IfStmt); !ok {
		t.Fatalf("modifier produced %T, want *ast.IfStmt", program.Statements[0])
	}
}

// A bare `assert` with no arguments stays a plain identifier read.
func TestAssertBareIdentifier(t *testing.T) {
	t.Parallel()
	program, errs := parseSource(t, `assert`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	stmt, ok := program.Statements[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("parsed %T, want *ast.ExprStmt", program.Statements[0])
	}
	if _, ok := stmt.Expr.(*ast.Identifier); !ok {
		t.Fatalf("bare assert parsed as %T, want *ast.Identifier", stmt.Expr)
	}
}

func soleAssertCall(t *testing.T, program *ast.Program) *ast.CallExpr {
	t.Helper()
	if len(program.Statements) != 1 {
		t.Fatalf("parsed %d statements, want 1", len(program.Statements))
	}
	stmt, ok := program.Statements[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("parsed %T, want *ast.ExprStmt", program.Statements[0])
	}
	call, ok := stmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("parsed %T, want *ast.CallExpr", stmt.Expr)
	}
	ident, ok := call.Callee.(*ast.Identifier)
	if !ok || ident.Name != "assert" {
		t.Fatalf("callee = %v, want identifier assert", call.Callee)
	}
	return call
}
