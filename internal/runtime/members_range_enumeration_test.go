package runtime

import "testing"

func TestRangeEach(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want int64
	}{
		{"ascending inclusive", "(1..3).each { |i| acc = acc * 10 + i }", 123},
		{"ascending exclusive", "(1...4).each { |i| acc = acc * 10 + i }", 123},
		{"descending inclusive", "(3..1).each { |i| acc = acc * 10 + i }", 321},
		{"descending exclusive", "(3...0).each { |i| acc = acc * 10 + i }", 321},
		{"single element", "(5..5).each { |i| acc = acc * 10 + i }", 5},
		{"empty exclusive", "(1...1).each { |i| acc = acc * 10 + i }", 0},
		{"spanning zero", "(-2..2).each { |i| acc = acc + i }", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  acc = 0\n  " + tc.expr + "\n  acc\nend"
			got := callFunc(t, compileScript(t, source), "run", nil)
			if !got.Equal(NewInt(tc.want)) {
				t.Fatalf("%s = %v, want %d", tc.expr, got, tc.want)
			}
		})
	}
}

func TestRangeEachReturnsReceiver(t *testing.T) {
	t.Parallel()

	source := "def run()\n  r = (1..3)\n  r.each { |i| i }\nend"
	got := callFunc(t, compileScript(t, source), "run", nil)
	if got.Kind() != KindRange {
		t.Fatalf("range.each returned %v, want the range", got.Kind())
	}
}

func TestRangeStep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want int64
	}{
		{"step lands on end", "(1..10).step(3) { |i| acc = acc * 100 + i }", 1040710},
		{"step overshoots end", "(1..8).step(3) { |i| acc = acc * 100 + i }", 10407},
		{"step one yields all", "(1..3).step(1) { |i| acc = acc * 10 + i }", 123},
		{"step exclusive", "(1...10).step(3) { |i| acc = acc * 100 + i }", 10407},
		{"step descending", "(10..1).step(3) { |i| acc = acc * 100 + i }", 10070401},
		{"step larger than span", "(1..3).step(10) { |i| acc = acc * 10 + i }", 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  acc = 0\n  " + tc.expr + "\n  acc\nend"
			got := callFunc(t, compileScript(t, source), "run", nil)
			if !got.Equal(NewInt(tc.want)) {
				t.Fatalf("%s = %v, want %d", tc.expr, got, tc.want)
			}
		})
	}
}

func TestRangeStepSparseStrideRespectsStepQuota(t *testing.T) {
	t.Parallel()

	// A sparse stride over a wide span must only charge the sandbox step quota
	// for the values it actually yields, not for the skipped integers. Each
	// expression yields a handful of values, so a tiny step quota is ample; the
	// old implementation walked every intermediate integer and would exhaust the
	// quota long before completing.
	tests := []struct {
		name string
		expr string
		want int64
	}{
		// Yields 1, 500001, 1000001: the trailing value lands one past End and is
		// excluded, so acc = 1 + 500001 = 500002.
		{"ascending sparse stride", "(1..1000000).step(500000) { |i| acc = acc + i }", 500002},
		// Yields 1000000, 500000, 0: the trailing value lands one before End and is
		// excluded, so acc = 1000000 + 500000 = 1500000.
		{"descending sparse stride", "(1000000..1).step(500000) { |i| acc = acc + i }", 1500000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  acc = 0\n  " + tc.expr + "\n  acc\nend"
			script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: 64 << 20}, source)
			got := callFunc(t, script, "run", nil)
			if !got.Equal(NewInt(tc.want)) {
				t.Fatalf("%s = %v, want %d", tc.expr, got, tc.want)
			}
		})
	}
}

func TestRangeMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want []Value
	}{
		{"double ascending", "(1..3).map { |i| i * 2 }", []Value{NewInt(2), NewInt(4), NewInt(6)}},
		{"exclusive", "(1...3).map { |i| i }", []Value{NewInt(1), NewInt(2)}},
		{"descending", "(3..1).map { |i| i }", []Value{NewInt(3), NewInt(2), NewInt(1)}},
		{"empty exclusive", "(1...1).map { |i| i }", []Value{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalRangeExpr(t, tc.expr)
			compareArrays(t, got, tc.want)
		})
	}
}

func TestRangeSelectReject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want []Value
	}{
		{"select even", "(1..6).select { |i| i.even? }", []Value{NewInt(2), NewInt(4), NewInt(6)}},
		{"reject even", "(1..6).reject { |i| i.even? }", []Value{NewInt(1), NewInt(3), NewInt(5)}},
		{"select none", "(1..3).select { |i| i > 10 }", []Value{}},
		{"reject all", "(1..3).reject { |i| i > 0 }", []Value{}},
		{"select descending", "(6..1).select { |i| i.odd? }", []Value{NewInt(5), NewInt(3), NewInt(1)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalRangeExpr(t, tc.expr)
			compareArrays(t, got, tc.want)
		})
	}
}

func TestRangeFind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want Value
	}{
		{"first over five", "(1..10).find { |i| i > 5 }", NewInt(6)},
		{"first even", "(1..10).find { |i| i.even? }", NewInt(2)},
		{"none matches", "(1..3).find { |i| i > 10 }", NewNil()},
		{"descending first", "(10..1).find { |i| i < 5 }", NewInt(4)},
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

func TestRangeReduce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want Value
	}{
		{"sum no seed", "(1..4).reduce { |a, b| a + b }", NewInt(10)},
		{"product seed", "(1..4).reduce(1) { |a, b| a * b }", NewInt(24)},
		{"seed used for empty", "(1...1).reduce(7) { |a, b| a + b }", NewInt(7)},
		{"empty no seed nil", "(1...1).reduce { |a, b| a + b }", NewNil()},
		{"single no seed", "(5..5).reduce { |a, b| a + b }", NewInt(5)},
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

func TestRangeCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want int64
	}{
		{"no block inclusive", "(1..5).count", 5},
		{"no block exclusive", "(1...5).count", 4},
		{"no block descending", "(5..1).count", 5},
		{"block odd", "(1..10).count { |i| i.odd? }", 5},
		{"block none", "(1..3).count { |i| i > 10 }", 0},
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

func TestRangeSum(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want int64
	}{
		{"inclusive", "(1..5).sum", 15},
		{"exclusive", "(1...5).sum", 10},
		{"with initial", "(1..3).sum(10)", 16},
		{"descending same total", "(5..1).sum", 15},
		{"empty exclusive", "(1...1).sum", 0},
		{"empty with initial", "(1...1).sum(4)", 4},
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

func TestRangeMinMax(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want Value
	}{
		{"min ascending", "(1..5).min", NewInt(1)},
		{"max ascending", "(1..5).max", NewInt(5)},
		{"max exclusive", "(1...5).max", NewInt(4)},
		{"min descending", "(5..1).min", NewInt(1)},
		{"max descending", "(5..1).max", NewInt(5)},
		{"min empty", "(1...1).min", NewNil()},
		{"max empty", "(1...1).max", NewNil()},
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

func TestRangeEnumerationArgumentRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"each no block", "(1..3).each", "requires a block"},
		{"each with arg", "(1..3).each(2) { |i| i }", "does not take arguments"},
		{"step no block", "(1..3).step(2)", "requires a block"},
		{"step no arg", "(1..3).step { |i| i }", "expects one integer argument"},
		{"step zero", "(1..3).step(0) { |i| i }", "must be positive"},
		{"step negative", "(1..3).step(-1) { |i| i }", "must be positive"},
		{"step float", "(1..3).step(1.5) { |i| i }", "expects an integer step"},
		{"step kwarg", "(1..3).step(1, by: 2) { |i| i }", "does not take keyword arguments"},
		{"map no block", "(1..3).map", "requires a block"},
		{"map with arg", "(1..3).map(2) { |i| i }", "does not take arguments"},
		{"select no block", "(1..3).select", "requires a block"},
		{"reject no block", "(1..3).reject", "requires a block"},
		{"find no block", "(1..3).find", "requires a block"},
		{"reduce too many args", "(1..3).reduce(0, 1) { |a, b| a + b }", "at most one argument"},
		{"reduce no block", "(1..3).reduce(0)", "requires a block"},
		{"count with arg", "(1..3).count(2)", "does not take arguments"},
		{"sum too many args", "(1..3).sum(0, 1)", "at most one argument"},
		{"sum non-int initial", "(1..3).sum(\"x\")", "integer initial value"},
		{"sum block", "(1..3).sum { |i| i }", "does not take a block"},
		{"min with block", "(1..3).min { |a, b| a }", "does not accept a block"},
		{"max with arg", "(1..3).max(2)", "does not take arguments"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  " + tc.expr + "\nend"
			requireCallErrorContains(t, compileScript(t, source), "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestRangeSumOverflow(t *testing.T) {
	t.Parallel()

	// A range whose total overflows int64 must raise rather than wrap. The
	// near-MaxInt64 span is injected as a data-only argument because the literal
	// would be unwieldy; sum iterates one step per element, so cap the step quota
	// to keep the test fast while still reaching the overflow.
	source := "def run()\n  (1..100000).sum(9223372036854775807)\nend"
	script := compileScriptWithConfig(t, Config{StepQuota: 200000, MemoryQuotaBytes: 64 << 20}, source)
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "overflow")
}

func TestRangeEnumerationStepQuota(t *testing.T) {
	t.Parallel()

	// Each yielded element consumes a step, so a wide range trips the step limit
	// across every iterating helper rather than running unbounded.
	tests := []string{
		"(1..1000000).each { |i| i }",
		"(1..1000000).map { |i| i }",
		"(1..1000000).select { |i| i.even? }",
		"(1..1000000).reject { |i| i.even? }",
		"(1..1000000).find { |i| i > 2000000 }",
		"(1..1000000).reduce(0) { |a, b| a + b }",
		"(1..1000000).count { |i| i.even? }",
		"(1..1000000).sum",
		"(1..1000000).min",
		"(1..1000000).max",
		"(1..1000000).step(1) { |i| i }",
	}
	for _, expr := range tests {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  " + expr + "\nend"
			script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: 64 << 20}, source)
			requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
		})
	}
}

func TestRangeMapMemoryQuota(t *testing.T) {
	t.Parallel()

	// Accumulating a mapped result honors the memory quota: a wide range whose
	// mapped array would exceed the quota trips the sandbox limit instead of
	// allocating unbounded memory.
	source := "def run()\n  (1..100000).map { |i| i }\nend"
	script := compileScriptWithConfig(t, Config{StepQuota: 200000, MemoryQuotaBytes: 4096}, source)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

func TestRangeSelectMemoryQuota(t *testing.T) {
	t.Parallel()

	// select accumulates kept elements, so a wide range whose result exceeds the
	// quota trips the sandbox limit.
	source := "def run()\n  (1..100000).select { |i| true }\nend"
	script := compileScriptWithConfig(t, Config{StepQuota: 200000, MemoryQuotaBytes: 4096}, source)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}
