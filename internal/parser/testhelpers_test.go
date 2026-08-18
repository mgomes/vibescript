package parser

import (
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

// parseSource is the standard parser test helper: it parses source with a
// fresh parser instance and returns the resulting program together with any
// parse errors. Tests that expect no errors should assert that before
// inspecting the program.
func parseSource(t testing.TB, source string) (*ast.Program, []error) {
	t.Helper()
	return newParser(source).parseProgram()
}

// callFromLastStatement digs the CallExpr out of the last statement of the
// first function in the program, unwrapping expression and assignment
// statements.
func callFromLastStatement(t *testing.T, program *ast.Program) *ast.CallExpr {
	t.Helper()
	fn, ok := program.Statements[0].(*ast.FunctionStmt)
	if !ok {
		t.Fatalf("first statement is %T, want *ast.FunctionStmt", program.Statements[0])
	}
	last := fn.Body[len(fn.Body)-1]
	var expr ast.Expression
	switch typed := last.(type) {
	case *ast.ExprStmt:
		expr = typed.Expr
	case *ast.AssignStmt:
		expr = typed.Value
	default:
		t.Fatalf("last statement is %T, want expression or assignment", last)
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expression is %T, want *ast.CallExpr", expr)
	}
	return call
}
