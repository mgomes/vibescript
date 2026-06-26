package runtime

import (
	"context"
	"errors"
	"testing"
)

// TestIndexAwareIterationHappyPaths pins the yielded values and indices for the
// array and hash index-aware helpers, including the sorted key order the hash
// helpers visit entries in.
func TestIndexAwareIterationHappyPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name:   "array each_with_index",
			source: `def run(); out = []; ["a", "b", "c"].each_with_index do |value, index| out = out.push([value, index]) end; out; end`,
			want: []Value{
				NewArray([]Value{NewString("a"), NewInt(0)}),
				NewArray([]Value{NewString("b"), NewInt(1)}),
				NewArray([]Value{NewString("c"), NewInt(2)}),
			},
		},
		{
			name:   "array map_with_index",
			source: `def run(); ["a", "b", "c"].map_with_index do |value, index| [value, index] end; end`,
			want: []Value{
				NewArray([]Value{NewString("a"), NewInt(0)}),
				NewArray([]Value{NewString("b"), NewInt(1)}),
				NewArray([]Value{NewString("c"), NewInt(2)}),
			},
		},
		{
			name:   "array map_with_index index-only projection",
			source: `def run(); [10, 20, 30].map_with_index do |value, index| value + index end; end`,
			want:   []Value{NewInt(10), NewInt(21), NewInt(32)},
		},
		{
			// Ruby's Hash#each_with_index yields the [key, value] pair plus the
			// index; Vibescript visits entries in sorted key order so the index is
			// deterministic regardless of insertion order.
			name:   "hash each_with_index pair and index in sorted order",
			source: `def run(); out = []; { b: 2, a: 1, c: 3 }.each_with_index do |pair, index| out = out.push([pair, index]) end; out; end`,
			want: []Value{
				NewArray([]Value{NewArray([]Value{NewSymbol("a"), NewInt(1)}), NewInt(0)}),
				NewArray([]Value{NewArray([]Value{NewSymbol("b"), NewInt(2)}), NewInt(1)}),
				NewArray([]Value{NewArray([]Value{NewSymbol("c"), NewInt(3)}), NewInt(2)}),
			},
		},
		{
			name:   "hash map_with_index pair and index in sorted order",
			source: `def run(); { b: 2, a: 1, c: 3 }.map_with_index do |pair, index| [pair[0], pair[1], index] end; end`,
			want: []Value{
				NewArray([]Value{NewSymbol("a"), NewInt(1), NewInt(0)}),
				NewArray([]Value{NewSymbol("b"), NewInt(2), NewInt(1)}),
				NewArray([]Value{NewSymbol("c"), NewInt(3), NewInt(2)}),
			},
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

// TestIndexAwareIterationEmptyReceivers covers the boundary case of an empty
// receiver: the block never runs, each_with_index returns the receiver, and
// map_with_index returns an empty array.
func TestIndexAwareIterationEmptyReceivers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name:   "array each_with_index empty",
			source: `def run(); [].each_with_index do |v, i| v end; end`,
			want:   []Value{},
		},
		{
			name:   "array map_with_index empty",
			source: `def run(); [].map_with_index do |v, i| v end; end`,
			want:   []Value{},
		},
		{
			name:   "hash map_with_index empty",
			source: `def run(); {}.map_with_index do |pair, i| pair end; end`,
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

// TestIndexAwareIterationReturnValues pins the Ruby return semantics: the
// each_with_index helpers return the receiver, while the map_with_index helpers
// return a freshly built array.
func TestIndexAwareIterationReturnValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   Value
	}{
		{
			name:   "array each_with_index returns receiver",
			source: `def run(); [1, 2, 3].each_with_index do |v, i| v + i end; end`,
			want:   NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		},
		{
			name:   "hash each_with_index returns receiver",
			source: `def run(); { a: 1, b: 2 }.each_with_index do |pair, i| pair end; end`,
			want:   NewHash(map[string]Value{"a": NewInt(1), "b": NewInt(2)}),
		},
		{
			name:   "hash each_with_index empty returns receiver",
			source: `def run(); {}.each_with_index do |pair, i| pair end; end`,
			want:   NewHash(map[string]Value{}),
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

// TestIndexAwareIterationErrors verifies argument and block validation for the
// index-aware helpers.
func TestIndexAwareIterationErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "array each_with_index without block",
			source: `def run(); [1, 2].each_with_index; end`,
			want:   "array.each_with_index requires a block",
		},
		{
			name:   "array each_with_index with arguments",
			source: `def run(); [1, 2].each_with_index(1) do |v, i| v end; end`,
			want:   "array.each_with_index does not take arguments",
		},
		{
			name:   "array map_with_index without block",
			source: `def run(); [1, 2].map_with_index; end`,
			want:   "array.map_with_index requires a block",
		},
		{
			name:   "array map_with_index with arguments",
			source: `def run(); [1, 2].map_with_index(1) do |v, i| v end; end`,
			want:   "array.map_with_index does not take arguments",
		},
		{
			name:   "hash each_with_index without block",
			source: `def run(); { a: 1 }.each_with_index; end`,
			want:   "hash.each_with_index requires a block",
		},
		{
			name:   "hash each_with_index with arguments",
			source: `def run(); { a: 1 }.each_with_index(1) do |pair, i| pair end; end`,
			want:   "hash.each_with_index does not take arguments",
		},
		{
			name:   "hash map_with_index without block",
			source: `def run(); { a: 1 }.map_with_index; end`,
			want:   "hash.map_with_index requires a block",
		},
		{
			name:   "hash map_with_index with arguments",
			source: `def run(); { a: 1 }.map_with_index(1) do |pair, i| pair end; end`,
			want:   "hash.map_with_index does not take arguments",
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

// TestIndexAwareIterationBlockErrorsPropagate ensures errors raised inside the
// yielded block bubble out unchanged.
func TestIndexAwareIterationBlockErrorsPropagate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "array each_with_index block error",
			source: `def run(); [1, 2].each_with_index do |v, i| v.frobnicate end; end`,
		},
		{
			name:   "array map_with_index block error",
			source: `def run(); [1, 2].map_with_index do |v, i| v.frobnicate end; end`,
		},
		{
			name:   "hash each_with_index block error",
			source: `def run(); { a: 1 }.each_with_index do |pair, i| pair.frobnicate end; end`,
		},
		{
			name:   "hash map_with_index block error",
			source: `def run(); { a: 1 }.map_with_index do |pair, i| pair.frobnicate end; end`,
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

// TestIndexAwareIterationMissingIndexParam confirms that omitting the index
// block parameter binds it to nil rather than raising, matching how Vibescript
// binds absent block parameters elsewhere.
func TestIndexAwareIterationMissingIndexParam(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `def run(); ["a", "b"].each_with_index do |value| value end; end`)
	compareArrays(t, callFunc(t, script, "run", nil), []Value{NewString("a"), NewString("b")})
}

// TestIndexAwareIterationParticipatesInStepQuota proves a tight step quota trips
// while the index-aware helpers walk a large receiver.
func TestIndexAwareIterationParticipatesInStepQuota(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		args   []Value
	}{
		{
			name:   "array each_with_index",
			source: `def run(values); values.each_with_index do |v, i| v end; end`,
			args:   []Value{largeIntArray(1000)},
		},
		{
			name:   "array map_with_index",
			source: `def run(values); values.map_with_index do |v, i| v end; end`,
			args:   []Value{largeIntArray(1000)},
		},
		{
			// An empty block body never steps on its own, so the explicit per-entry
			// step is what bounds the walk over a large hash.
			name:   "hash each_with_index empty block",
			source: `def run(values); values.each_with_index do |pair, i| end; end`,
			args:   []Value{largeHashReceiver(1000)},
		},
		{
			name:   "hash map_with_index empty block",
			source: `def run(values); values.map_with_index do |pair, i| end; end`,
			args:   []Value{largeHashReceiver(1000)},
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

// TestIndexAwareIterationHonorsCancellation confirms a canceled context stops
// the index-aware helpers, including the hash helpers with an empty block body
// that relies on the explicit per-entry step for cancellation.
func TestIndexAwareIterationHonorsCancellation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "array each_with_index",
			source: `def run(); [1, 2, 3].each_with_index do |v, i| v end; end`,
		},
		{
			name:   "array map_with_index",
			source: `def run(); [1, 2, 3].map_with_index do |v, i| v end; end`,
		},
		{
			name:   "hash each_with_index empty block",
			source: `def run(); { a: 1, b: 2, c: 3 }.each_with_index do |pair, i| end; end`,
		},
		{
			name:   "hash map_with_index empty block",
			source: `def run(); { a: 1, b: 2, c: 3 }.map_with_index do |pair, i| end; end`,
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
