package parser

import (
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

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

// TestParserAmpersandBlockArgument pins Ruby-style ampersand block arguments:
// `&expr` forwarding and `&:name` symbol-to-proc in parenthesized and
// parenless call positions.
func TestParserAmpersandBlockArgument(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		source     string
		wantSymbol bool
	}{
		{
			name: "block_forwarding",
			source: `def run
  mapper = nil
  [1, 2].map(&mapper)
end`,
		},
		{
			name: "symbol_to_proc",
			source: `def run
  ["a", "b"].map(&:upcase)
end`,
			wantSymbol: true,
		},
		{
			name: "parenless_block_forwarding",
			source: `def run
  mapper = nil
  values.map &mapper
end`,
		},
		{
			name: "with_positional_argument",
			source: `def run
  mapper = nil
  values.fetch(0, &mapper)
end`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			program, errs := parseSource(t, tc.source)
			if len(errs) != 0 {
				t.Fatalf("parseSource(%q) errors: %v", tc.source, errs)
			}
			call := callFromLastStatement(t, program)
			if call.BlockArg == nil {
				t.Fatal("call has no BlockArg")
			}
			if call.Block != nil {
				t.Fatal("call must not also carry a literal block")
			}
			if _, ok := call.BlockArg.(*ast.SymbolLiteral); ok != tc.wantSymbol {
				t.Fatalf("BlockArg is %T, wantSymbol=%v", call.BlockArg, tc.wantSymbol)
			}
		})
	}
}

// TestParserAmpersandBlockArgumentErrors pins the block-argument placement
// rules: it must be the last argument, appear once, and cannot be combined
// with a literal block.
func TestParserAmpersandBlockArgumentErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "not_last",
			source: `def run
  m(&blk, 1)
end`,
			want: "block argument must be the last argument",
		},
		{
			name: "duplicate",
			source: `def run
  m(&a, &b)
end`,
			want: "block argument must be the last argument",
		},
		{
			name: "with_literal_block",
			source: `def run
  m(&blk) { 1 }
end`,
			want: "cannot pass both a block argument and a literal block",
		},
		{
			name: "with_do_block",
			source: `def run
  m(&blk) do
    1
  end
end`,
			want: "cannot pass both a block argument and a literal block",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, errs := parseSource(t, tc.source)
			if len(errs) == 0 {
				t.Fatalf("parseSource(%q) expected errors", tc.source)
			}
			found := false
			for _, err := range errs {
				if strings.Contains(err.Error(), tc.want) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("parseSource(%q) errors = %v, want substring %q", tc.source, errs, tc.want)
			}
		})
	}
}
