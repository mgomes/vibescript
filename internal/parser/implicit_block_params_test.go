package parser

import (
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserInfersImplicitBlockParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []string
	}{
		{
			name:   "numbered",
			source: `def run; [1].map { _1 * 2 }; end`,
			want:   []string{"_1"},
		},
		{
			name:   "higher_numbered",
			source: `def run; ["a"].map_with_index { _2 }; end`,
			want:   []string{"_2"},
		},
		{
			name:   "it",
			source: `def run; [1].map { it * 2 }; end`,
			want:   []string{"it"},
		},
		{
			name:   "explicit_params_disable_implicit",
			source: `def run; [1].map { |it| it * 2 }; end`,
			want:   nil,
		},
		{
			name:   "explicit_empty_brace_params_disable_implicit",
			source: `def run; [1].map { || _1 }; end`,
			want:   nil,
		},
		{
			name:   "explicit_empty_do_params_disable_implicit",
			source: `def run; [1].map do || _1 end; end`,
			want:   nil,
		},
		{
			name:   "it_callee_stays_callable",
			source: `def run; [1].map { it(_1) }; end`,
			want:   []string{"_1"},
		},
		{
			name:   "nested_block_isolated",
			source: `def run; [1].map { [2].map { _1 } }; end`,
			want:   nil,
		},
		{
			name:   "assigned_name_not_implicit",
			source: `def run; [1].map { it = 10; it }; end`,
			want:   nil,
		},
		{
			name:   "member_assignment_target_uses_it",
			source: `def run; items.each { it.name = "x" }; end`,
			want:   []string{"it"},
		},
		{
			name:   "index_assignment_target_uses_numbered",
			source: `def run; items.each { _1[:seen] = true }; end`,
			want:   []string{"_1"},
		},
		{
			name:   "rescue_binding_scoped_inside_handler",
			source: `def run; [1].map { begin; raise "x"; rescue => it; nil; end; it }; end`,
			want:   []string{"it"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, errs := parseSource(t, tc.source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", tc.source, errs)
			}

			block := firstFunctionCallBlock(t, got)
			if diff := stringSlicesDiff(tc.want, block.ImplicitParams); diff != "" {
				t.Fatalf("implicit params mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func firstFunctionCallBlock(t *testing.T, program *ast.Program) *ast.BlockLiteral {
	t.Helper()
	for _, stmt := range parsedFunctionBody(t, program) {
		block := firstStatementCallBlock(stmt)
		if block != nil {
			return block
		}
	}
	t.Fatal("function body has no call block")
	return nil
}

func firstStatementCallBlock(stmt ast.Statement) *ast.BlockLiteral {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		return firstExpressionCallBlock(s.Expr)
	case *ast.AssignStmt:
		return firstExpressionCallBlock(s.Value)
	default:
		return nil
	}
}

func firstExpressionCallBlock(expr ast.Expression) *ast.BlockLiteral {
	switch e := expr.(type) {
	case *ast.CallExpr:
		if e.Block != nil {
			return e.Block
		}
		if block := firstExpressionCallBlock(e.Callee); block != nil {
			return block
		}
		for _, arg := range e.Args {
			if block := firstExpressionCallBlock(arg); block != nil {
				return block
			}
		}
		for _, arg := range e.KwArgs {
			if block := firstExpressionCallBlock(arg.Value); block != nil {
				return block
			}
		}
	case *ast.MemberExpr:
		return firstExpressionCallBlock(e.Object)
	case *ast.ArrayLiteral:
		for _, element := range e.Elements {
			if block := firstExpressionCallBlock(element); block != nil {
				return block
			}
		}
	}
	return nil
}

func stringSlicesDiff(want, got []string) string {
	if len(want) != len(got) {
		return formatStringSliceDiff(want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			return formatStringSliceDiff(want, got)
		}
	}
	return ""
}

func formatStringSliceDiff(want, got []string) string {
	return "want: " + formatStringSlice(want) + "\ngot:  " + formatStringSlice(got)
}

func formatStringSlice(values []string) string {
	if values == nil {
		return "nil"
	}
	out := "["
	for i, value := range values {
		if i > 0 {
			out += " "
		}
		out += value
	}
	return out + "]"
}
