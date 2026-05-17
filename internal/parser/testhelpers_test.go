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
