package runtime

import (
	"context"
	"errors"
	goruntime "runtime"
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
		{
			// A nil count is the explicit unbounded form, matching Ruby's
			// cycle(nil); it must loop forever just like the omitted-count
			// case, so the step quota is what stops it.
			name:   "cycle nil count is unbounded",
			source: `def run(); [1, 2].cycle(nil) do |v| end; end`,
			args:   nil,
		},
		{
			// Forwarding an optional count that defaults to nil must behave
			// like the unbounded form rather than raising.
			name:   "cycle forwarded nil count is unbounded",
			source: `def run(); count = nil; [1, 2].cycle(count) do |v| end; end`,
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

// TestArrayEachNestedRestEmptyBodyTripsMemoryQuota mirrors the Hash#each nested-rest
// escape on the array side: a |(head, *tail)| block over an array whose elements are
// themselves arrays binds tail to a fresh, source-sized copy of each element. With an
// EMPTY block body the body's own memory checks never run, and the receiver -- driven
// directly into the builtin here -- lives only on the Go call stack, invisible to
// estimateMemoryUsageBase. Without the per-call bind charge the array and hash
// iterators now share, the fresh copy would escape the quota; the charge counts it
// against the live call roots so a quota that fits the receiver but not the tail copy
// must reject the walk. This is the array-side twin of
// TestHashEachEmptyBodyNestedRestTripsMemoryQuota and likewise fails without the fix.
func TestArrayEachNestedRestEmptyBodyTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	pos := Position{Line: 1, Column: 1}
	// |(head, *tail)| with an empty body: only the bind charge can observe the fresh
	// tail copy each element yields.
	target := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "head", Position: pos}},
			{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
		},
	}
	block := NewBlock([]Param{{Kind: ParamNormal, Target: target}}, nil, newEnv(nil))

	// One element whose payload dwarfs the headroom: binding its tail copies the whole
	// element into a fresh backing the quota cannot hold.
	const elementLen = 200_000
	element := make([]Value, elementLen)
	for i := range element {
		element[i] = NewInt(int64(i))
	}
	receiver := NewArray([]Value{NewArray(element)})

	// Size the quota to admit the live roots (receiver plus block) and a little
	// headroom, but not the fresh tail copy the rest collects.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	quota := roots + 16*1024

	member, err := arrayMember(receiver, "each")
	if err != nil {
		t.Fatalf("arrayMember(each): %v", err)
	}
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err = valueBuiltin(member).Fn(exec, receiver, nil, nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestArrayEachNestedRestRejectsBeforeAllocatingTail pins the #808 P1 fix: a
// rest-collecting block parameter must reject an over-quota tail BEFORE the
// destructurer materializes it, not after. assignDestructure copies a named rest's
// window into a fresh make([]Value, len(window)) backing; the bind charge now
// preflights that window against the quota before the copy. Without the preflight a
// quota below a single copied tail still produced errMemoryQuotaExceeded -- but only
// AFTER the make+copy already allocated the full tail (the OOM/escape the finding
// flagged). This test sizes the quota below one tail copy and measures the bytes the
// rejected walk allocates: a pre-allocation gate keeps that far below the tail's
// backing, while a post-allocation check would allocate the whole tail before
// rejecting, failing the byte ceiling. It is the array-side companion to
// TestArrayEachNestedRestEmptyBodyTripsMemoryQuota, which only asserts the rejection.
func TestArrayEachNestedRestRejectsBeforeAllocatingTail(t *testing.T) {
	// Not parallel: this test reads process-wide allocation counters and must not
	// race other goroutines' allocations.

	pos := Position{Line: 1, Column: 1}
	// |(head, *tail)| with an EMPTY body: only the bind charge observes the fresh
	// tail copy, so the preflight is the sole gate before the make+copy.
	target := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "head", Position: pos}},
			{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
		},
	}
	block := NewBlock([]Param{{Kind: ParamNormal, Target: target}}, nil, newEnv(nil))

	// One element whose tail dwarfs the quota: binding |(head, *tail)| would copy
	// tailLen Value slots into a fresh backing. tailLen = elementLen - 1 (head takes
	// the first slot).
	const elementLen = 2_000_000
	const tailLen = elementLen - 1
	element := make([]Value, elementLen)
	for i := range element {
		element[i] = NewInt(int64(i))
	}
	receiver := NewArray([]Value{NewArray(element)})

	// Size the quota to admit the live call roots (receiver plus block) plus a little
	// headroom, but far below the fresh tail backing the rest would collect. Both a
	// pre- and a post-allocation check reject here; the distinguishing signal is the
	// bytes allocated before the rejection, measured below.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	quota := roots + 16*1024

	member, err := arrayMember(receiver, "each")
	if err != nil {
		t.Fatalf("arrayMember(each): %v", err)
	}
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	_, err = valueBuiltin(member).Fn(exec, receiver, nil, nil, block)
	goruntime.ReadMemStats(&after)

	requireErrorIs(t, err, errMemoryQuotaExceeded)

	// A post-allocation check would make+copy the whole tail (tailLen Value slots)
	// before rejecting. The pre-allocation gate never reaches that make, so the
	// rejected walk allocates only bookkeeping. Use a generous ceiling (a tenth of
	// the tail backing) to stay robust against unrelated allocation noise while still
	// failing loudly if the full tail is ever materialized before the check.
	const fullTailBytes = uint64(tailLen) * uint64(estimatedValueBytes)
	const ceiling = fullTailBytes / 10
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > ceiling {
		t.Fatalf("rejected walk allocated %d bytes, want <= %d (a copied tail would be %d): the rest window was materialized before the quota check",
			allocated, ceiling, fullTailBytes)
	}
}

// TestCallBlockNestedRestArgTripsMemoryQuota covers the capability-adapter side of
// the positional-call-root gap the Codex thread on PR #808 raised. A host capability
// drives a user block through exec.CallBlock with arguments that live only on the Go
// call stack; a |(head, *tail)| block binds tail to a fresh, source-sized copy of the
// argument array. With an EMPTY block body only the per-call bind charge observes the
// copy, and that charge must count the argument it was copied from. The quota admits
// the fresh tail copy charged against an empty baseline (the buggy path, which omitted
// the host arguments) plus headroom, but NOT the real peak (argument + tail). Before
// the fix the buggy baseline let CallBlock complete and escape the quota.
func TestCallBlockNestedRestArgTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	pos := Position{Line: 1, Column: 1}
	target := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "head", Position: pos}},
			{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
		},
	}
	block := NewBlock([]Param{{Kind: ParamNormal, Target: target}}, nil, newEnv(nil))

	const argLen = 200_000
	argValue := arrayValue(argLen)
	args := []Value{argValue}

	// begin seeds the estimator with the host arguments, so the fresh tail copy's
	// shared int payloads deduplicate and only its genuinely new backing slots are
	// charged. Mirror that to size the quota from the estimator's real accounting.
	est := newMemoryEstimator()
	est.value(argValue)
	tail := NewArray(slicesClone(argValue.Array()[1:]))
	tailCharge := est.value(tail)
	const headroom = 16 * 1024
	// The buggy baseline omitted the host arguments entirely (CallBlock passes no
	// receiver), so it charged only the fresh tail. A quota that clears that buggy
	// peak with headroom would have let the call complete.
	quota := tailCharge + headroom

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	correctRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), NewNil(), args, nil, block)
	if correctRoots+tailCharge <= quota {
		t.Fatalf("test setup expects the argument-inclusive peak (%d) to exceed the quota (%d)", correctRoots+tailCharge, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := exec.CallBlock(block, args)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}
