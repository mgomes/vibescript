package runtime

import "testing"

func TestRangeEachAndStep(t *testing.T) {
	t.Parallel()

	// each, each_with_index, and step return the receiver, so the yielded values
	// are observed through a reassigned accumulator, the value-semantics-safe way.
	tests := []struct {
		name string
		expr string
		want int64
	}{
		{"each ascending", "total = 0\n  (1..4).each { |i| total = total + i }\n  total", 10},
		{"each exclusive", "total = 0\n  (1...4).each { |i| total = total + i }\n  total", 6},
		{"each descending", "total = 0\n  (4..1).each { |i| total = total + i }\n  total", 10},
		{"each single inclusive", "total = 0\n  (3..3).each { |i| total = total + i }\n  total", 3},
		{"each empty exclusive", "total = 99\n  (1...1).each { |i| total = i }\n  total", 99},
		{"each returns receiver start", "(5..9).each { |i| i }.first", 5},
		{"each_with_index weights", "total = 0\n  (10..12).each_with_index { |v, i| total = total + v * i }\n  total", 35},
		{"step ascending lands", "total = 0\n  (1..10).step(3) { |i| total = total + i }\n  total", 22},
		{"step ascending stops short", "total = 0\n  (1..9).step(3) { |i| total = total + i }\n  total", 12},
		{"step descending", "total = 0\n  (10..1).step(3) { |i| total = total + i }\n  total", 22},
		{"step of one is each", "total = 0\n  (1..4).step(1) { |i| total = total + i }\n  total", 10},
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

func TestRangeMapSelectReject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want []Value
	}{
		{"map doubles", "(1..3).map { |i| i * 2 }", []Value{NewInt(2), NewInt(4), NewInt(6)}},
		{"map exclusive", "(1...4).map { |i| i }", []Value{NewInt(1), NewInt(2), NewInt(3)}},
		{"map descending", "(3..1).map { |i| i }", []Value{NewInt(3), NewInt(2), NewInt(1)}},
		{"map empty exclusive", "(1...1).map { |i| i }", []Value{}},
		{"map non-int results", "(1..3).map { |i| \"n#{i}\" }", []Value{NewString("n1"), NewString("n2"), NewString("n3")}},
		{"select evens", "(1..6).select { |i| i.even? }", []Value{NewInt(2), NewInt(4), NewInt(6)}},
		{"reject evens", "(1..6).reject { |i| i.even? }", []Value{NewInt(1), NewInt(3), NewInt(5)}},
		{"select none", "(1..3).select { |i| i > 9 }", []Value{}},
		{"reject all", "(1..3).reject { |i| i > 0 }", []Value{}},
		{"select descending preserves order", "(6..1).select { |i| i.even? }", []Value{NewInt(6), NewInt(4), NewInt(2)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalRangeExpr(t, tc.expr)
			compareArrays(t, got, tc.want)
		})
	}
}

func TestRangeFold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want int64
	}{
		{"reduce operation string", "(1..5).reduce(\"+\")", 15},
		{"reduce operation product", "(1..5).reduce(\"*\")", 120},
		{"reduce initial and operation", "(1..5).reduce(100, \"+\")", 115},
		{"reduce block", "(1..5).reduce { |a, b| a + b }", 15},
		{"reduce initial and block", "(1..5).reduce(10) { |a, b| a + b }", 25},
		{"reduce descending", "(5..1).reduce(\"+\")", 15},
		{"sum default", "(1..5).sum", 15},
		{"sum initial", "(1..5).sum(100)", 115},
		{"sum block", "(1..3).sum { |i| i * 2 }", 12},
		{"sum descending", "(5..1).sum", 15},
		{"count no arg", "(1..5).count", 5},
		{"count exclusive", "(1...5).count", 4},
		{"count value present", "(1..5).count(3)", 1},
		{"count value absent", "(1..5).count(9)", 0},
		{"count block", "(1..6).count { |i| i.even? }", 3},
		{"count descending", "(5..1).count", 5},
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

func TestRangeFoldNilAndFind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want Value
	}{
		{"reduce empty no seed", "(1...1).reduce(\"+\")", NewNil()},
		{"reduce empty with seed", "(1...1).reduce(7) { |a, b| a + b }", NewInt(7)},
		{"sum empty default", "(1...1).sum", NewInt(0)},
		{"sum empty initial", "(1...1).sum(5)", NewInt(5)},
		{"count empty", "(1...1).count", NewInt(0)},
		{"find first match", "(1..10).find { |i| i > 4 }", NewInt(5)},
		{"find descending", "(10..1).find { |i| i < 4 }", NewInt(3)},
		{"find none", "(1..3).find { |i| i > 9 }", NewNil()},
		{"find empty", "(1...1).find { |i| true }", NewNil()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalRangeExpr(t, tc.expr)
			if !got.Equal(tc.want) {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestRangeEnumArgumentRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"each no block", "(1..3).each", "requires a block"},
		{"each with arg", "(1..3).each(1) { |i| i }", "does not take arguments"},
		{"each_with_index no block", "(1..3).each_with_index", "requires a block"},
		{"map no block", "(1..3).map", "requires a block"},
		{"map with arg", "(1..3).map(1) { |i| i }", "does not take arguments"},
		{"select no block", "(1..3).select", "requires a block"},
		{"reject no block", "(1..3).reject", "requires a block"},
		{"find no block", "(1..3).find", "requires a block"},
		{"reduce no block or op", "(1..3).reduce", "requires a block or an operation"},
		{"reduce bad operation", "(1..3).reduce(5)", "must be a symbol or string"},
		{"reduce too many args", "(1..3).reduce(1, \"+\", 2)", "at most an initial value and an operation"},
		{"reduce kwarg", "(1..3).reduce(limit: 2)", "does not take keyword arguments"},
		{"sum too many args", "(1..3).sum(1, 2)", "at most an initial value"},
		{"sum non-numeric initial", "(1..3).sum(\"x\")", "must be numeric"},
		{"sum kwarg", "(1..3).sum(limit: 2)", "does not take keyword arguments"},
		{"count too many args", "(1..3).count(1, 2)", "at most one value argument"},
		{"step no arg", "(1..3).step { |i| i }", "expects a step size"},
		{"step no block", "(1..3).step(2)", "requires a block"},
		{"step non-int", "(1..3).step(2.5) { |i| i }", "integer step"},
		{"step zero", "(1..3).step(0) { |i| i }", "must be positive"},
		{"step negative", "(1..3).step(-1) { |i| i }", "must be positive"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestRangeSumBlockNonNumericRejected(t *testing.T) {
	t.Parallel()
	script := compileScript(t, "def run()\n  (1..3).sum { |i| \"x\" }\nend")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "must return a numeric value")
}

func TestRangeEnumStepQuota(t *testing.T) {
	t.Parallel()

	// Each yielded value charges a step, so a large range trips the step limit
	// rather than iterating unbounded, including when the result accumulates.
	tests := []struct {
		name string
		expr string
	}{
		{"each", "(1..1000000).each { |i| i }"},
		{"map", "(1..1000000).map { |i| i }"},
		{"select", "(1..1000000).select { |i| true }"},
		{"reduce", "(1..1000000).reduce(\"+\")"},
		{"sum", "(1..1000000).sum"},
		{"count block", "(1..1000000).count { |i| true }"},
		{"step", "(1..1000000).step(1) { |i| i }"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  " + tc.expr + "\nend"
			script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: 64 << 20}, source)
			requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
		})
	}
}

func TestRangeMapMemoryQuota(t *testing.T) {
	t.Parallel()

	// A map that builds large per-element strings must trip the memory quota
	// while accumulating rather than only after returning the whole array.
	source := "def run()\n  (1..100000).map { |i| \"padding-#{i}\" }\nend"
	script := compileScriptWithConfig(t, Config{StepQuota: 1000000, MemoryQuotaBytes: 8192}, source)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

func TestRangeSelectMemoryQuota(t *testing.T) {
	t.Parallel()

	// select keeps integers, so its result memory is bounded by the int-array
	// projection; a large kept result still trips the memory quota.
	source := "def run()\n  (1..100000).select { |i| true }\nend"
	script := compileScriptWithConfig(t, Config{StepQuota: 1000000, MemoryQuotaBytes: 4096}, source)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

func TestRangeEachOverflowSafe(t *testing.T) {
	t.Parallel()

	// An inclusive range whose end is MaxInt64 must terminate after yielding the
	// final element rather than wrapping the counter and looping forever. last(n)
	// is derived arithmetically, so build the range as a literal and count a short
	// prefix via find to prove the loop advances and can stop near the boundary.
	source := "def run()\n  (9223372036854775805..9223372036854775807).find { |i| i == 9223372036854775807 }\nend"
	script := compileScriptWithConfig(t, Config{StepQuota: 1000, MemoryQuotaBytes: 64 << 20}, source)
	got := callFunc(t, script, "run", nil)
	if !got.Equal(NewInt(9223372036854775807)) {
		t.Fatalf("find near MaxInt64 = %v, want 9223372036854775807", got)
	}
}
