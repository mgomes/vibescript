package runtime

import (
	"context"
	"math"
	goruntime "runtime"
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
		// Zero-arg helpers must reject stray keyword arguments before doing any
		// work, matching the predicate and first/last helpers. Without this guard
		// (1..N).to_a(limit: 10) would materialize the whole range.
		{"size with kwarg", "(1..3).size(limit: 10)", "does not take keyword arguments"},
		{"exclude_end with kwarg", "(1..3).exclude_end?(limit: 10)", "does not take keyword arguments"},
		{"to_a with kwarg", "(1..1000000).to_a(limit: 10)", "does not take keyword arguments"},
		{"cover with kwarg", "(1..3).cover?(2, limit: 10)", "does not take keyword arguments"},
		{"first with kwarg", "(1..5).first(2, limit: 10)", "does not take keyword arguments"},
		{"last with kwarg", "(1..5).last(2, limit: 10)", "does not take keyword arguments"},
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

func TestRangeMaterializeRejectsHugePreallocation(t *testing.T) {
	t.Parallel()

	// A near-MaxInt64 range must trip the memory quota up front rather than
	// reserving its full backing array. Before the projected-size check the
	// make([]Value, 0, int(limit)) call would reserve tens of gigabytes for the
	// capacity (panicking or OOMing the host) before any per-element memory
	// check observed the allocation. Both to_a and first(n) flow through the
	// same materializer, so cover both entry points.
	tests := []struct {
		name string
		expr string
	}{
		{"to_a", "(0...9223372036854775807).to_a"},
		{"first", "(0...9223372036854775807).first(9000000000000000000)"},
		{"last", "(0...9223372036854775807).last(9000000000000000000)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  " + tc.expr + "\nend"
			script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 * 1024}, source)
			requireRunMemoryQuotaError(t, script, nil, CallOptions{})
		})
	}
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

func TestRangeToArrayStepQuotaWithoutPreallocation(t *testing.T) {
	t.Parallel()

	// A small StepQuota must trip the step limit even when MemoryQuotaBytes is
	// large enough that the projected up-front check passes. Reserving capacity
	// for every requested element here would allocate ~1.6 GiB for the backing
	// array before the per-element step() could reject the call. The materializer
	// instead grows the backing array via append, so the actual allocation stays
	// proportional to the few elements the step quota allows and the call fails
	// with the step limit rather than an out-of-memory condition.
	source := `def run()
  (1..100000000).to_a
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: 8 << 30}, source)
	requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
}

func TestRangeMaterializeBoundsInitialCapacity(t *testing.T) {
	// Not parallel: this test reads process-wide allocation counters and must
	// not race other goroutines' allocations.

	// A large requested limit must not reserve its full capacity up front: the
	// materializer caps the initial allocation and lets append grow the backing
	// array as elements are produced. The limit here passes the projected memory
	// check (so execution reaches the loop) but is far larger than the step
	// quota, so the call trips the step limit after materializing only a handful
	// of elements. Reserving capacity for the full limit beforehand would
	// allocate limit*sizeof(Value) bytes regardless of how few steps the quota
	// permits; the bounded growth path keeps the allocation proportional to the
	// elements actually produced.
	const limit = int64(100_000_000)
	exec := &Execution{
		ctx:         context.Background(),
		quota:       50,
		memoryQuota: 8 << 30,
	}
	rng := Range{Start: 0, End: math.MaxInt64, Exclusive: true}

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	_, err := exec.rangeMaterialize(rng, limit, false)
	goruntime.ReadMemStats(&after)

	requireErrorIs(t, err, errStepQuotaExceeded)
	if exec.steps > exec.quota+1 {
		t.Fatalf("steps = %d, want the loop to stop near the step quota %d", exec.steps, exec.quota)
	}

	// The full preallocation would have reserved limit*sizeof(Value) bytes. The
	// bounded path materializes only ~quota elements before failing, so its
	// allocation is many orders of magnitude smaller. Use a generous ceiling to
	// stay robust against unrelated allocation noise while still failing loudly
	// if the full capacity is ever reserved again.
	const fullPreallocBytes = uint64(limit) * uint64(estimatedValueBytes)
	const ceiling = fullPreallocBytes / 1000
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > ceiling {
		t.Fatalf("materialize allocated %d bytes, want <= %d (full preallocation would be %d)",
			allocated, ceiling, fullPreallocBytes)
	}
}

func TestRangeMaterializeGrowsPastInitialCapacity(t *testing.T) {
	t.Parallel()

	// Materializing more elements than the bounded initial capacity must grow the
	// backing array correctly, proving the append-driven growth path produces the
	// full, correct sequence rather than truncating at the initial capacity.
	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 30,
		memoryQuota: 1 << 30,
	}
	count := int64(rangeMaterializeInitialCap) + 1000
	rng := Range{Start: 1, End: count, Exclusive: false}
	result, err := exec.rangeMaterialize(rng, count, false)
	if err != nil {
		t.Fatalf("rangeMaterialize: %v", err)
	}
	arr := result.Array()
	if int64(len(arr)) != count {
		t.Fatalf("len = %d, want %d", len(arr), count)
	}
	for i := range arr {
		if want := int64(i) + 1; arr[i].Int() != want {
			t.Fatalf("element %d = %d, want %d", i, arr[i].Int(), want)
		}
	}
}

func TestRangeLastBoundedOnHugeRange(t *testing.T) {
	t.Parallel()

	// last(n) computes the trailing window arithmetically rather than iterating
	// (and leaving uncharged) the skipped prefix. On a range whose length is
	// near MaxInt64 this returns instantly; a regression that walked the prefix
	// would hang or trip the step quota before producing a result.
	source := `def run()
  (0...9223372036854775807).last(3)
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 100000, MemoryQuotaBytes: 64 << 20}, source)
	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindArray {
		t.Fatalf("last(3) kind = %v, want array", got.Kind())
	}
	want := []int64{9223372036854775804, 9223372036854775805, 9223372036854775806}
	arr := got.Array()
	if len(arr) != len(want) {
		t.Fatalf("last(3) = %v, want length %d", arr, len(want))
	}
	for i, w := range want {
		if arr[i].Int() != w {
			t.Fatalf("last(3)[%d] = %d, want %d", i, arr[i].Int(), w)
		}
	}
}

func TestRangeBoundedOnOverflowingLength(t *testing.T) {
	t.Parallel()

	// The inclusive full positive span has length MaxInt64+1, which overflows
	// int64. A bounded first(n)/last(n) only needs to allocate n elements, so it
	// must succeed instead of being rejected as "result too large". last(n) is
	// derived arithmetically from the end endpoint and so stays bounded even
	// though Ruby itself would OOM materializing this range.
	tests := []struct {
		name string
		expr string
		want []int64
	}{
		{"first one", "(0..9223372036854775807).first(1)", []int64{0}},
		{"first three", "(0..9223372036854775807).first(3)", []int64{0, 1, 2}},
		{"last one", "(0..9223372036854775807).last(1)", []int64{math.MaxInt64}},
		{
			"last three",
			"(0..9223372036854775807).last(3)",
			[]int64{math.MaxInt64 - 2, math.MaxInt64 - 1, math.MaxInt64},
		},
		{"first zero", "(0..9223372036854775807).first(0)", nil},
		{"last zero", "(0..9223372036854775807).last(0)", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  " + tc.expr + "\nend"
			script := compileScriptWithConfig(t, Config{StepQuota: 100000, MemoryQuotaBytes: 64 << 20}, source)
			requireRangeInts(t, callFunc(t, script, "run", nil), tc.want)
		})
	}
}

func TestRangeBoundedOnSpanOverflow(t *testing.T) {
	t.Parallel()

	// MinInt64..MaxInt64 cannot be written as a literal (the parser rejects the
	// MinInt64 token), but it is a valid range value: high - low overflows int64,
	// so rangeLength reports overflow. Bounded first(n)/last(n) must still work.
	// Inject the range as a data-only global and exercise both endpoints.
	fullSpan := NewRange(Range{Start: math.MinInt64, End: math.MaxInt64})
	tests := []struct {
		name string
		expr string
		want []int64
	}{
		{"first two", "span.first(2)", []int64{math.MinInt64, math.MinInt64 + 1}},
		{"last two", "span.last(2)", []int64{math.MaxInt64 - 1, math.MaxInt64}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run(span)\n  " + tc.expr + "\nend"
			script := compileScriptWithConfig(t, Config{StepQuota: 100000, MemoryQuotaBytes: 64 << 20}, source)
			result, err := script.Call(context.Background(), "run", []Value{fullSpan}, CallOptions{})
			if err != nil {
				t.Fatalf("call run: %v", err)
			}
			requireRangeInts(t, result, tc.want)
		})
	}
}

func requireRangeInts(t *testing.T, got Value, want []int64) {
	t.Helper()
	if got.Kind() != KindArray {
		t.Fatalf("kind = %v, want array", got.Kind())
	}
	arr := got.Array()
	if len(arr) != len(want) {
		t.Fatalf("length = %d, want %d (%v)", len(arr), len(want), arr)
	}
	for i, w := range want {
		if arr[i].Kind() != KindInt || arr[i].Int() != w {
			t.Fatalf("element %d = %v, want %d", i, arr[i], w)
		}
	}
}

func TestRangeLastStepQuota(t *testing.T) {
	t.Parallel()

	// Each element of the trailing window consumes a step, so last(n) with a
	// large window stops on the step limit rather than materializing unbounded.
	source := `def run()
  (1..100000).last(60000)
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: 64 << 20}, source)
	requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
}
