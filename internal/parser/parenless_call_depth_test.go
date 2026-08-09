package parser

import (
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

// A parenless call parses its argument as a full expression, and that argument
// may be another parenless call, so a line of identifiers nests one call per
// identifier. 500,000 of them fit in half the default MaxSourceBytes and
// overflowed the goroutine stack, which is a fatal the host cannot recover from
// and cannot be blamed on the script (#47).

func parenlessCallLine(identifiers int) string {
	return "def run\n  " + strings.TrimSpace(strings.Repeat("a ", identifiers)) + "\nend\n"
}

// parenlessCallNesting counts the calls stacked inside the body of the function
// the source above declares, which is one per identifier past the first.
func parenlessCallNesting(t *testing.T, program *ast.Program) int {
	t.Helper()

	if len(program.Statements) != 1 {
		t.Fatalf("expected one declaration, got %d", len(program.Statements))
	}
	fn, ok := program.Statements[0].(*ast.FunctionStmt)
	if !ok || len(fn.Body) != 1 {
		t.Fatalf("expected a function of one statement, got %T", program.Statements[0])
	}
	stmt, ok := fn.Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected an expression statement, got %T", fn.Body[0])
	}

	depth := 0
	expr := stmt.Expr
	for {
		call, ok := expr.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return depth
		}
		depth++
		expr = call.Args[0]
	}
}

// The cap has to be reachable, or it would be bounding something other than
// what it claims to: a line that nests exactly maxParenlessCallDepth calls still
// parses, and parses whole rather than stopping short.
func TestParenlessCallNestingAtTheCapParses(t *testing.T) {
	t.Parallel()

	program, errs := Parse(parenlessCallLine(maxParenlessCallDepth + 1))
	if len(errs) != 0 {
		t.Fatalf("nesting %d parenless calls no longer parses: %v", maxParenlessCallDepth, errs[0])
	}
	if depth := parenlessCallNesting(t, program); depth != maxParenlessCallDepth {
		t.Fatalf("parsed %d nested calls, want %d", depth, maxParenlessCallDepth)
	}
}

// One past the cap is a parse error naming the nesting, not a shorter parse
// that quietly drops the rest of the line.
func TestParenlessCallNestingPastTheCapIsRejected(t *testing.T) {
	t.Parallel()

	_, errs := Parse(parenlessCallLine(maxParenlessCallDepth + 2))
	if len(errs) != 1 {
		t.Fatalf("nesting %d parenless calls produced %d diagnostics, want 1", maxParenlessCallDepth+1, len(errs))
	}
	if !strings.Contains(errs[0].Error(), "parenless call nesting too deep") {
		t.Fatalf("diagnostic does not name the nesting: %v", errs[0])
	}
}

// The one that matters: a line long enough to exhaust the stack has to come
// back as diagnostics. A stack overflow is fatal rather than a panic, so there
// is nothing to recover from -- failing this takes the whole test binary with
// it, which is the point.
func TestDeepParenlessCallNestingDoesNotCrashTheHost(t *testing.T) {
	t.Parallel()

	// 1,000,014 bytes, just inside the 1 MiB default MaxSourceBytes, so the
	// engine's own size guard never sees it. It took 203ms to reject; before,
	// it overflowed the 1 GB goroutine stack, and 200,000 identifiers already
	// took 10s to parse.
	_, errs := Parse(parenlessCallLine(500_000))
	if len(errs) == 0 {
		t.Fatal("500,000 nested parenless calls parsed without a diagnostic, so nothing bounds the recursion")
	}
	if !strings.Contains(errs[0].Error(), "parenless call nesting too deep") {
		t.Fatalf("first diagnostic does not name the nesting: %v", errs[0])
	}
}
