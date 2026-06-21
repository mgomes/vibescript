package runtime

import (
	"context"
	"errors"
	"testing"
)

// TestArrayIterationHelpers covers the happy paths for the Ruby-style block
// iteration helpers, including the short trailing slice, sliding windows, and
// the bounded cycle repetition shown in the issue example.
func TestArrayIterationHelpers(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      each_slice = []
      [1, 2, 3, 4, 5].each_slice(2) do |slice|
        each_slice = each_slice.push(slice)
      end
      each_cons = []
      [1, 2, 3, 4].each_cons(3) do |window|
        each_cons = each_cons.push(window)
      end
      reverse_each = []
      [1, 2, 3].reverse_each do |value|
        reverse_each = reverse_each.push(value)
      end
      cycle = []
      [1, 2].cycle(2) do |value|
        cycle = cycle.push(value)
      end
      {
        each_slice: each_slice,
        each_cons: each_cons,
        reverse_each: reverse_each,
        cycle: cycle,
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()

	compareArrays(t, got["each_slice"], []Value{
		NewArray([]Value{NewInt(1), NewInt(2)}),
		NewArray([]Value{NewInt(3), NewInt(4)}),
		NewArray([]Value{NewInt(5)}),
	})
	compareArrays(t, got["each_cons"], []Value{
		NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		NewArray([]Value{NewInt(2), NewInt(3), NewInt(4)}),
	})
	compareArrays(t, got["reverse_each"], []Value{NewInt(3), NewInt(2), NewInt(1)})
	compareArrays(t, got["cycle"], []Value{NewInt(1), NewInt(2), NewInt(1), NewInt(2)})
}

// TestArrayIterationHelperEdges captures the empty and boundary behaviors that
// differ from a naive implementation: short receivers, exact-fit windows, and
// the non-positive cycle counts that Ruby treats as a no-op.
func TestArrayIterationHelperEdges(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name:   "each_slice empty receiver",
			source: `def run(); acc = []; [].each_slice(2) do |s| acc = acc.push(s) end; acc; end`,
			want:   []Value{},
		},
		{
			name:   "each_slice size larger than receiver",
			source: `def run(); acc = []; [1, 2].each_slice(5) do |s| acc = acc.push(s) end; acc; end`,
			want:   []Value{NewArray([]Value{NewInt(1), NewInt(2)})},
		},
		{
			name:   "each_cons window equals length",
			source: `def run(); acc = []; [1, 2, 3].each_cons(3) do |w| acc = acc.push(w) end; acc; end`,
			want:   []Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})},
		},
		{
			name:   "each_cons window larger than length",
			source: `def run(); acc = []; [1, 2].each_cons(3) do |w| acc = acc.push(w) end; acc; end`,
			want:   []Value{},
		},
		{
			name:   "reverse_each empty receiver",
			source: `def run(); acc = []; [].reverse_each do |v| acc = acc.push(v) end; acc; end`,
			want:   []Value{},
		},
		{
			name:   "cycle zero count",
			source: `def run(); acc = []; [1, 2].cycle(0) do |v| acc = acc.push(v) end; acc; end`,
			want:   []Value{},
		},
		{
			name:   "cycle negative count",
			source: `def run(); acc = []; [1, 2].cycle(-3) do |v| acc = acc.push(v) end; acc; end`,
			want:   []Value{},
		},
		{
			name:   "cycle empty receiver",
			source: `def run(); acc = []; [].cycle(5) do |v| acc = acc.push(v) end; acc; end`,
			want:   []Value{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			compareArrays(t, callFunc(t, script, "run", nil), tc.want)
		})
	}
}

// TestArrayIterationHelperReturnValues pins the return values to Ruby's: the
// each_* slice/window helpers and cycle return nil, while reverse_each returns
// the receiver.
func TestArrayIterationHelperReturnValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   Value
	}{
		{
			name:   "each_slice returns nil",
			source: `def run(); [1, 2, 3].each_slice(2) do |s| s end; end`,
			want:   NewNil(),
		},
		{
			name:   "each_cons returns nil",
			source: `def run(); [1, 2, 3].each_cons(2) do |w| w end; end`,
			want:   NewNil(),
		},
		{
			name:   "cycle returns nil",
			source: `def run(); [1, 2].cycle(2) do |v| v end; end`,
			want:   NewNil(),
		},
		{
			name:   "reverse_each returns receiver",
			source: `def run(); [1, 2, 3].reverse_each do |v| v end; end`,
			want:   NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			got := callFunc(t, script, "run", nil)
			if diff := valuesDiff([]Value{tc.want}, []Value{got}); diff != "" {
				t.Fatalf("return value mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestArrayIterationHelperErrors verifies the argument and block validation,
// including the Ruby-aligned "invalid slice size" / "invalid size" messages and
// the no-block requirement.
func TestArrayIterationHelperErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "each_slice without block",
			source: `def run(); [1, 2].each_slice(2); end`,
			want:   "array.each_slice requires a block",
		},
		{
			name:   "each_slice without size",
			source: `def run(); [1, 2].each_slice do |s| s end; end`,
			want:   "array.each_slice expects a slice size",
		},
		{
			name:   "each_slice zero size",
			source: `def run(); [1, 2].each_slice(0) do |s| s end; end`,
			want:   "array.each_slice invalid slice size",
		},
		{
			name:   "each_slice negative size",
			source: `def run(); [1, 2].each_slice(-1) do |s| s end; end`,
			want:   "array.each_slice invalid slice size",
		},
		{
			name:   "each_slice non-integer size",
			source: `def run(); [1, 2].each_slice("2") do |s| s end; end`,
			want:   "array.each_slice invalid slice size",
		},
		{
			name:   "each_cons without block",
			source: `def run(); [1, 2].each_cons(2); end`,
			want:   "array.each_cons requires a block",
		},
		{
			name:   "each_cons without size",
			source: `def run(); [1, 2].each_cons do |w| w end; end`,
			want:   "array.each_cons expects a window size",
		},
		{
			name:   "each_cons zero size",
			source: `def run(); [1, 2].each_cons(0) do |w| w end; end`,
			want:   "array.each_cons invalid size",
		},
		{
			name:   "each_cons non-integer size",
			source: `def run(); [1, 2].each_cons(2.5) do |w| w end; end`,
			want:   "array.each_cons invalid size",
		},
		{
			name:   "reverse_each without block",
			source: `def run(); [1, 2].reverse_each; end`,
			want:   "array.reverse_each requires a block",
		},
		{
			name:   "reverse_each with arguments",
			source: `def run(); [1, 2].reverse_each(1) do |v| v end; end`,
			want:   "array.reverse_each does not take arguments",
		},
		{
			name:   "cycle without block",
			source: `def run(); [1, 2].cycle(2); end`,
			want:   "array.cycle requires a block",
		},
		{
			name:   "cycle non-integer count",
			source: `def run(); [1, 2].cycle("2") do |v| v end; end`,
			want:   "array.cycle count must be an integer",
		},
		{
			name:   "cycle too many arguments",
			source: `def run(); [1, 2].cycle(1, 2) do |v| v end; end`,
			want:   "array.cycle accepts at most one count",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// TestArrayIterationHelperBlockErrorsPropagate ensures errors raised inside the
// yielded block bubble out unchanged.
func TestArrayIterationHelperBlockErrorsPropagate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "each_slice block error",
			source: `def run(); [1, 2].each_slice(1) do |s| s.frobnicate end; end`,
		},
		{
			name:   "each_cons block error",
			source: `def run(); [1, 2].each_cons(1) do |w| w.frobnicate end; end`,
		},
		{
			name:   "reverse_each block error",
			source: `def run(); [1, 2].reverse_each do |v| v.frobnicate end; end`,
		},
		{
			name:   "cycle block error",
			source: `def run(); [1, 2].cycle(2) do |v| v.frobnicate end; end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "frobnicate")
		})
	}
}

// TestArrayIterationHelpersParticipateInStepQuota proves a tight step quota
// trips while the helpers walk a large receiver, including an unbounded cycle
// whose explicit per-yield step keeps it from running forever.
func TestArrayIterationHelpersParticipateInStepQuota(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		args   []Value
	}{
		{
			name:   "each_slice",
			source: `def run(values); values.each_slice(1) do |s| s end; end`,
			args:   []Value{largeIntArray(1000)},
		},
		{
			name:   "each_cons",
			source: `def run(values); values.each_cons(1) do |w| w end; end`,
			args:   []Value{largeIntArray(1000)},
		},
		{
			name:   "reverse_each",
			source: `def run(values); values.reverse_each do |v| v end; end`,
			args:   []Value{largeIntArray(1000)},
		},
		{
			// An empty block body never steps on its own, so the explicit
			// per-yield step is what bounds the otherwise infinite cycle.
			name:   "cycle infinite empty block",
			source: `def run(); [1, 2].cycle do |v| end; end`,
			args:   nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: 40}, tc.source)
			requireCallRuntimeErrorType(t, script, "run", tc.args, CallOptions{}, runtimeErrorTypeLimit)
		})
	}
}

// TestArrayIterationHelpersHonorCancellation confirms a canceled context stops
// the helpers, including a cycle with an empty block body that relies on the
// explicit per-yield step for cancellation.
func TestArrayIterationHelpersHonorCancellation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "each_slice",
			source: `def run(); [1, 2, 3].each_slice(1) do |s| s end; end`,
		},
		{
			name:   "each_cons",
			source: `def run(); [1, 2, 3].each_cons(1) do |w| w end; end`,
		},
		{
			name:   "reverse_each",
			source: `def run(); [1, 2, 3].reverse_each do |v| v end; end`,
		},
		{
			name:   "cycle empty block",
			source: `def run(); [1, 2, 3].cycle(100) do |v| end; end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := script.Call(ctx, "run", nil, CallOptions{})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s under canceled context = %v, want context.Canceled", tc.name, err)
			}
		})
	}
}

// TestArrayIterationHelpersIsolateYieldedSlices guards against yielded slices
// or windows aliasing the receiver's backing array: mutating a yielded element
// must not change the receiver.
func TestArrayIterationHelpersIsolateYieldedSlices(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def slice_isolation()
      values = [1, 2, 3, 4]
      values.each_slice(2) do |slice|
        slice[0] = 99
      end
      values
    end

    def cons_isolation()
      values = [1, 2, 3, 4]
      values.each_cons(2) do |window|
        window[0] = 99
      end
      values
    end
    `)

	compareArrays(t, callFunc(t, script, "slice_isolation", nil), []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})
	compareArrays(t, callFunc(t, script, "cons_isolation", nil), []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})
}
