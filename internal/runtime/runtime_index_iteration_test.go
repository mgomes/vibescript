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

// arrayIndexIterationBuiltin returns the raw builtin for an array index-aware
// helper so tests can drive it directly against a pre-built Go-level receiver,
// bypassing the call-frame binding that would otherwise mask a missing
// per-yield step or memory charge behind a quota trip on the receiver itself.
func arrayIndexIterationBuiltin(t *testing.T, method string) BuiltinFunc {
	t.Helper()
	member, err := arrayMember(NewArray(nil), method)
	if err != nil {
		t.Fatalf("arrayMember(%s): %v", method, err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("%s member is not a builtin: %v", method, member.Kind())
	}
	return builtin.Fn
}

// TestArrayIndexIterationEmptyBlockParticipatesInStepQuota guards against an
// empty block body letting each_with_index or map_with_index traverse the whole
// receiver without charging the step quota. runner.call only charges steps for
// the statements it evaluates, and an empty block evaluates none, so each helper
// must charge a step per yield itself.
//
// The builtin is driven directly against a pre-built Go-level receiver under a
// generous memory quota so the only thing that can trip the limit is the
// per-yield step. A script-level test cannot isolate this: passing a large
// receiver trips the memory quota while binding the call frame, masking a
// missing per-yield step. The assertion targets errStepQuotaExceeded
// specifically so a memory-quota trip cannot satisfy it.
func TestArrayIndexIterationEmptyBlockParticipatesInStepQuota(t *testing.T) {
	t.Parallel()
	const receiverSize = 1000
	const stepQuota = 40
	for _, method := range []string{"each_with_index", "map_with_index"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			fn := arrayIndexIterationBuiltin(t, method)
			exec := &Execution{
				ctx:         context.Background(),
				quota:       stepQuota,
				memoryQuota: 8 << 30,
			}
			_, err := fn(exec, largeIntArray(receiverSize), nil, nil, emptyBlockValue())
			requireErrorIs(t, err, errStepQuotaExceeded)
			if exec.steps > stepQuota+1 {
				t.Fatalf("steps = %d, want the loop to stop near the step quota %d", exec.steps, stepQuota)
			}
		})
	}
}

// TestArrayIndexIterationEmptyBlockHonorsCancellation guards against an empty
// block body letting the array index-aware helpers ignore context
// cancellation. The per-yield step also observes the canceled context, so the
// call must abort rather than run to completion.
//
// As with the step-quota test, the builtin is driven directly: a script-level
// canceled-context test trips on the first step charged while evaluating the
// function body, before the loop runs, so it would pass even if the helper never
// checked cancellation. Driving the builtin against a pre-built receiver makes
// the loop the only place a cancellation can be observed.
func TestArrayIndexIterationEmptyBlockHonorsCancellation(t *testing.T) {
	t.Parallel()
	const receiverSize = 1000
	for _, method := range []string{"each_with_index", "map_with_index"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			fn := arrayIndexIterationBuiltin(t, method)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			exec := &Execution{
				ctx:         ctx,
				quota:       1 << 30,
				memoryQuota: 8 << 30,
			}
			_, err := fn(exec, largeIntArray(receiverSize), nil, nil, emptyBlockValue())
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s with empty block under canceled context = %v, want context.Canceled", method, err)
			}
			if exec.steps >= receiverSize {
				t.Fatalf("steps = %d, want the loop to abort early on cancellation", exec.steps)
			}
		})
	}
}

// TestArrayMapWithIndexChargesAccumulatedResultsDuringIteration guards against
// the accumulating result array escaping the memory quota while the loop runs.
// The result slice is local, so it is invisible to step()'s slow-path
// checkMemory() and to the post-call checkMemoryWith(result); a block that
// allocates a fresh large value per element could otherwise pile up far beyond
// MemoryQuotaBytes before the post-call check observed the returned array.
// map_with_index must charge the accumulating result on every element so the
// limit trips mid-loop, exactly like filter_map and hash.map_with_index.
//
// The builtin is driven directly against a small Go-level receiver so the
// receiver itself costs no call-binding memory: the only thing that can trip the
// limit is the results the block produces. Asserting the loop aborts early
// (steps well below the receiver length) proves the per-element check caught the
// growth rather than the post-call check, which would only fire after the whole
// receiver had been traversed.
func TestArrayMapWithIndexChargesAccumulatedResultsDuringIteration(t *testing.T) {
	t.Parallel()
	const receiverSize = 4096
	const resultWidth = 256
	fn := arrayIndexIterationBuiltin(t, "map_with_index")

	oneResult := newMemoryEstimator().value(freshIntArrayValue(resultWidth))
	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 30,
		memoryQuota: oneResult * 8,
	}
	_, err := fn(exec, largeIntArray(receiverSize), nil, nil, freshArrayBlockValue(resultWidth))
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps >= receiverSize {
		t.Fatalf("steps = %d, want the loop to trip the memory quota before traversing the whole receiver (%d)", exec.steps, receiverSize)
	}
}

// TestArrayMapWithIndexDoesNotOverchargeAliasedResults guards the incremental
// per-element memory charge against double-counting a backing every element
// shares. The block returns the same array on every call, so the post-call
// checkMemoryWith(result) counts that single backing once. Sizing the quota just
// above the post-call estimate proves the incremental charge deduplicates the
// shared backing exactly as the post-call check does: re-counting the backing
// per element would trip the quota and reject a result the post-call check
// accepts.
func TestArrayMapWithIndexDoesNotOverchargeAliasedResults(t *testing.T) {
	t.Parallel()
	const receiverSize = 64
	const sharedWidth = 32
	fn := arrayIndexIterationBuiltin(t, "map_with_index")

	shared := freshIntArrayValue(sharedWidth)
	sharedPayload := newMemoryEstimator().value(shared)
	arrayOverhead := estimatedValueBytes + estimatedSliceBaseBytes + receiverSize*estimatedValueBytes
	correctTotal := arrayOverhead + sharedPayload
	overCountedTotal := arrayOverhead + receiverSize*sharedPayload
	memoryQuota := correctTotal * 4
	if memoryQuota >= overCountedTotal {
		t.Fatalf("test quota %d does not distinguish correct (%d) from over-counted (%d)", memoryQuota, correctTotal, overCountedTotal)
	}
	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 30,
		memoryQuota: memoryQuota,
	}
	got, err := fn(exec, largeIntArray(receiverSize), nil, nil, sharedArrayBlockValue(shared))
	if err != nil {
		t.Fatalf("map_with_index over aliased results = %v, want success (incremental charge must dedup the shared backing)", err)
	}
	if got.Kind() != KindArray || len(got.Array()) != receiverSize {
		t.Fatalf("map_with_index produced %v, want %d aliased elements", got, receiverSize)
	}
}

// TestArrayMapWithIndexCountsDistinctBackings guards the incremental per-element
// memory charge against under-counting. Every element is a freshly allocated
// array with its own backing, so the charger must accumulate each element's
// payload; walking only the first element (or otherwise dropping later payloads)
// would let a dense result of distinct large values slip past the quota. The
// quota admits a few results but not all of them, so the loop must trip before
// traversing the whole receiver.
func TestArrayMapWithIndexCountsDistinctBackings(t *testing.T) {
	t.Parallel()
	const receiverSize = 4096
	const resultWidth = 256
	fn := arrayIndexIterationBuiltin(t, "map_with_index")

	oneResult := newMemoryEstimator().value(freshIntArrayValue(resultWidth))
	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 30,
		memoryQuota: oneResult * 8,
	}
	_, err := fn(exec, largeIntArray(receiverSize), nil, nil, freshArrayBlockValue(resultWidth))
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps >= receiverSize {
		t.Fatalf("steps = %d, want the loop to trip the memory quota before traversing the whole receiver (%d)", exec.steps, receiverSize)
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
