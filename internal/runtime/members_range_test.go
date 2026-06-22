package runtime

import (
	"testing"
)

func evalRangeExpr(t *testing.T, expr string) Value {
	t.Helper()
	script := compileScript(t, "def run()\n  "+expr+"\nend")
	return callFunc(t, script, "run", nil)
}

func TestRangeMembershipPredicates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"cover inclusive in", "(1..3).cover?(2)", true},
		{"cover inclusive end", "(1..3).cover?(3)", true},
		{"cover inclusive out", "(1..3).cover?(4)", false},
		{"cover exclusive end", "(1...3).cover?(3)", false},
		{"cover exclusive in", "(1...3).cover?(2)", true},
		{"include alias", "(1..3).include?(2)", true},
		{"include out", "(1..3).include?(0)", false},
		{"member alias", "(1..3).member?(1)", true},
		{"member out", "(1..3).member?(5)", false},
		{"cover float within", "(1..3).cover?(2.5)", true},
		{"cover float at start", "(1..3).cover?(1.0)", true},
		{"cover float below", "(1..3).cover?(0.5)", false},
		{"cover float above", "(1..3).cover?(3.5)", false},
		{"cover float exclusive end", "(1...3).cover?(2.5)", true},
		{"descending in", "(5..1).cover?(3)", true},
		{"descending out", "(5..1).cover?(6)", false},
		{"descending exclusive end", "(5...1).cover?(1)", false},
		{"non-numeric arg string", "(1..3).cover?(\"2\")", false},
		{"non-numeric arg nil", "(1..3).include?(nil)", false},
		{"non-numeric arg array", "(1..3).member?([2])", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalRangeExpr(t, tc.expr)
			if got.Kind() != KindBool {
				t.Fatalf("%s kind = %v, want bool", tc.expr, got.Kind())
			}
			if got.Bool() != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got.Bool(), tc.want)
			}
		})
	}
}

func TestRangeMetadataHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want int64
	}{
		{"first endpoint inclusive", "(1..3).first", 1},
		{"first endpoint exclusive", "(1...3).first", 1},
		{"last endpoint inclusive", "(1..3).last", 3},
		// last ignores exclusivity for the bare endpoint, matching Ruby.
		{"last endpoint exclusive", "(1...3).last", 3},
		{"first descending endpoint", "(5..1).first", 5},
		{"last descending endpoint", "(5..1).last", 1},
		{"size inclusive", "(1..3).size", 3},
		{"size exclusive", "(1...3).size", 2},
		{"size single inclusive", "(2..2).size", 1},
		{"size empty exclusive", "(1...1).size", 0},
		// Vibescript iterates descending ranges, so size reports the span
		// rather than Ruby's zero. See docs/control-flow.md.
		{"size descending inclusive", "(5..1).size", 5},
		{"size descending exclusive", "(5...1).size", 4},
		{"size spanning zero", "(-3..3).size", 7},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalRangeExpr(t, tc.expr)
			if !got.Equal(NewInt(tc.want)) {
				t.Fatalf("%s = %v, want %d", tc.expr, got, tc.want)
			}
		})
	}
}

func TestRangeExcludeEnd(t *testing.T) {
	t.Parallel()

	if got := evalRangeExpr(t, "(1..3).exclude_end?"); !got.Equal(NewBool(false)) {
		t.Fatalf("(1..3).exclude_end? = %v, want false", got)
	}
	if got := evalRangeExpr(t, "(1...3).exclude_end?"); !got.Equal(NewBool(true)) {
		t.Fatalf("(1...3).exclude_end? = %v, want true", got)
	}
	if got := evalRangeExpr(t, "(5..1).exclude_end?"); !got.Equal(NewBool(false)) {
		t.Fatalf("(5..1).exclude_end? = %v, want false", got)
	}
}

func TestRangeToArray(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want []Value
	}{
		{"inclusive ascending", "(1..3).to_a", []Value{NewInt(1), NewInt(2), NewInt(3)}},
		{"exclusive ascending", "(1...3).to_a", []Value{NewInt(1), NewInt(2)}},
		{"single inclusive", "(1..1).to_a", []Value{NewInt(1)}},
		{"empty exclusive", "(1...1).to_a", []Value{}},
		{"spanning zero", "(-2..2).to_a", []Value{NewInt(-2), NewInt(-1), NewInt(0), NewInt(1), NewInt(2)}},
		// Descending materialization mirrors descending for-loop iteration.
		{"descending inclusive", "(5..1).to_a", []Value{NewInt(5), NewInt(4), NewInt(3), NewInt(2), NewInt(1)}},
		{"descending exclusive", "(5...1).to_a", []Value{NewInt(5), NewInt(4), NewInt(3), NewInt(2)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalRangeExpr(t, tc.expr)
			compareArrays(t, got, tc.want)
		})
	}
}

func TestRangeFirstLastCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want []Value
	}{
		{"first n", "(1..5).first(2)", []Value{NewInt(1), NewInt(2)}},
		{"last n", "(1..5).last(2)", []Value{NewInt(4), NewInt(5)}},
		{"first clamps", "(1..3).first(10)", []Value{NewInt(1), NewInt(2), NewInt(3)}},
		{"last clamps", "(1..3).last(10)", []Value{NewInt(1), NewInt(2), NewInt(3)}},
		{"first zero", "(1..5).first(0)", []Value{}},
		{"last zero", "(1..5).last(0)", []Value{}},
		{"first exclusive", "(1...5).first(2)", []Value{NewInt(1), NewInt(2)}},
		{"last exclusive", "(1...5).last(2)", []Value{NewInt(3), NewInt(4)}},
		// Ruby clamps last(n) on exclusive ranges to the iterated elements.
		{"last exclusive clamps", "(1...5).last(10)", []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)}},
		{"first empty exclusive", "(1...1).first(3)", []Value{}},
		{"last empty exclusive", "(1...1).last(3)", []Value{}},
		// Descending first/last follow descending iteration order.
		{"first descending", "(5..1).first(2)", []Value{NewInt(5), NewInt(4)}},
		{"last descending", "(5..1).last(2)", []Value{NewInt(2), NewInt(1)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalRangeExpr(t, tc.expr)
			compareArrays(t, got, tc.want)
		})
	}
}

func TestRangeHelperArgumentRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"cover no arg", "(1..3).cover?", "expects one argument"},
		{"cover extra arg", "(1..3).cover?(1, 2)", "expects one argument"},
		{"include no arg", "(1..3).include?", "expects one argument"},
		{"member no arg", "(1..3).member?", "expects one argument"},
		{"size with arg", "(1..3).size(1)", "does not take arguments"},
		{"exclude_end with arg", "(1..3).exclude_end?(1)", "does not take arguments"},
		{"to_a with arg", "(1..3).to_a(1)", "does not take arguments"},
		{"first negative", "(1..5).first(-1)", "non-negative"},
		{"last negative", "(1..5).last(-1)", "non-negative"},
		{"first non-int", "(1..5).first(\"2\")", "integer count"},
		{"last non-int", "(1..5).last(2.5)", "integer count"},
		{"unknown method", "(1..3).reverse", "unknown range method"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestRangeToArrayMemoryQuota(t *testing.T) {
	t.Parallel()

	// Materializing a large range must trip the sandbox limit rather than
	// allocating unbounded memory. The quota is small relative to the array
	// of integers the range would produce.
	source := `def run()
  (1..100000).to_a
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 200000, MemoryQuotaBytes: 4096}, source)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

func TestRangeToArrayStepQuota(t *testing.T) {
	t.Parallel()

	// Each materialized element consumes a step, so a range larger than the
	// step quota stops on the step limit even with ample memory.
	source := `def run()
  (1..100000).to_a
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: 64 << 20}, source)
	requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
}
