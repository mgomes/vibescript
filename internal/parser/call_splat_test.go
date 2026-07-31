package parser

import (
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

// TestParserCallSplatArguments pins Ruby-style call argument expansion:
// positional splats (*args), keyword splats (**opts), combined forms, and
// mixing with regular arguments in parenthesized and parenless calls.
func TestParserCallSplatArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		source     string
		wantSplats int
		wantArgs   int
		wantKw     int
		wantKwSpl  int
	}{
		{
			name: "positional_splat",
			source: `def run
  sum(*xs)
end`,
			wantSplats: 1,
			wantArgs:   1,
		},
		{
			name: "keyword_splat",
			source: `def run
  fetch(**opts)
end`,
			wantKw:    1,
			wantKwSpl: 1,
		},
		{
			name: "nested_splat_with_trailing_argument",
			source: `def run
  sum(*[first, second], third)
end`,
			wantSplats: 1,
			wantArgs:   2,
		},
		{
			name: "combined_forwarding",
			source: `def run
  spawn(:prepare_user, *args, **opts)
end`,
			wantSplats: 1,
			wantArgs:   2,
			wantKw:     1,
			wantKwSpl:  1,
		},
		{
			name: "multiple_splats",
			source: `def run
  sum(*first, 1, *second)
end`,
			wantSplats: 2,
			wantArgs:   3,
		},
		{
			name: "parenless_splat",
			source: `def run
  sum *xs
end`,
			wantSplats: 1,
			wantArgs:   1,
		},
		{
			name: "parenless_continuation_splat",
			source: `def run
  sum 1, *xs
end`,
			wantSplats: 1,
			wantArgs:   2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			program, errs := parseSource(t, tc.source)
			if len(errs) != 0 {
				t.Fatalf("parseSource(%q) errors: %v", tc.source, errs)
			}
			call := callFromLastStatement(t, program)
			if len(call.Args) != tc.wantArgs {
				t.Fatalf("args = %d, want %d", len(call.Args), tc.wantArgs)
			}
			splats := 0
			for _, arg := range call.Args {
				if _, ok := arg.(*ast.SplatArg); ok {
					splats++
				}
			}
			if splats != tc.wantSplats {
				t.Fatalf("splat args = %d, want %d", splats, tc.wantSplats)
			}
			if len(call.KwArgs) != tc.wantKw {
				t.Fatalf("kwargs = %d, want %d", len(call.KwArgs), tc.wantKw)
			}
			kwSplats := 0
			for _, kw := range call.KwArgs {
				if kw.Splat {
					kwSplats++
				}
			}
			if kwSplats != tc.wantKwSpl {
				t.Fatalf("keyword splats = %d, want %d", kwSplats, tc.wantKwSpl)
			}
		})
	}
}

// TestParserSplatSpacingKeepsOperators pins the disambiguation: a local
// callee or operator spacing keeps * and ** as binary operators.
func TestParserSplatSpacingKeepsOperators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
	}{
		{name: "spaced_multiplication", expr: "values * other"},
		{name: "flush_multiplication", expr: "values*other"},
		{name: "block_pass_shape_after_local", expr: "values *other"},
		{name: "spaced_power", expr: "values ** other"},
		{name: "power_shape_after_local", expr: "values **other"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source := "def run\n  values = 6\n  other = 7\n  " + tc.expr + "\nend"
			program, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
			}
			fn := program.Statements[0].(*ast.FunctionStmt)
			stmt, ok := fn.Body[2].(*ast.ExprStmt)
			if !ok {
				t.Fatalf("statement is %T, want *ast.ExprStmt", fn.Body[2])
			}
			if _, ok := stmt.Expr.(*ast.BinaryExpr); !ok {
				t.Fatalf("expression is %T, want *ast.BinaryExpr", stmt.Expr)
			}
		})
	}
}

// TestParserSplatOrderingErrors pins that positional splats cannot follow
// keyword arguments, matching the plain positional rule.
func TestParserSplatOrderingErrors(t *testing.T) {
	t.Parallel()

	source := `def run
  f(k: 1, *args)
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatal("expected a parse error for splat after keyword argument")
	}
}
