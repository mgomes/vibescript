package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestArrayRejectTakeDropGrep(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      values = [1, 2, 3, 4]
      words = ["apple", "bee", "cat"]
      {
        reject: values.reject do |n|
          n % 2 == 0
        end,
        reject_empty: [].reject do |n|
          true
        end,
        take_while: values.take_while do |n|
          n < 3
        end,
        take_while_all: values.take_while do |n|
          n < 9
        end,
        take_while_none: values.take_while do |n|
          n < 0
        end,
        drop_while: values.drop_while do |n|
          n < 3
        end,
        drop_while_all: values.drop_while do |n|
          n < 9
        end,
        drop_while_none: values.drop_while do |n|
          n < 0
        end,
        grep_range: values.grep(2..3),
        grep_v_range: values.grep_v(2..3),
        grep_equal: words.grep("bee"),
        grep_v_equal: words.grep_v("bee"),
        grep_block: values.grep(2..3) do |n|
          n * 10
        end,
        grep_v_block: values.grep_v(2..3) do |n|
          n * 10
        end,
        original: values
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()

	compareArrays(t, got["reject"], []Value{NewInt(1), NewInt(3)})
	compareArrays(t, got["reject_empty"], []Value{})
	compareArrays(t, got["take_while"], []Value{NewInt(1), NewInt(2)})
	compareArrays(t, got["take_while_all"], []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})
	compareArrays(t, got["take_while_none"], []Value{})
	compareArrays(t, got["drop_while"], []Value{NewInt(3), NewInt(4)})
	compareArrays(t, got["drop_while_all"], []Value{})
	compareArrays(t, got["drop_while_none"], []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})
	compareArrays(t, got["grep_range"], []Value{NewInt(2), NewInt(3)})
	compareArrays(t, got["grep_v_range"], []Value{NewInt(1), NewInt(4)})
	compareArrays(t, got["grep_equal"], []Value{NewString("bee")})
	compareArrays(t, got["grep_v_equal"], []Value{NewString("apple"), NewString("cat")})
	compareArrays(t, got["grep_block"], []Value{NewInt(20), NewInt(30)})
	compareArrays(t, got["grep_v_block"], []Value{NewInt(10), NewInt(40)})
	compareArrays(t, got["original"], []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})
}

func TestArrayFilterMap(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      values = [1, 2, 3, 4]
      {
        even_times_ten: values.filter_map do |n|
          if n % 2 == 0 then n * 10 end
        end,
        all_kept: values.filter_map do |n|
          n * 2
        end,
        none_kept: values.filter_map do |n|
          nil
        end,
        empty: [].filter_map do |n|
          n
        end,
        drops_false: [1, 2, 3].filter_map do |n|
          n == 2
        end,
        original: values
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()

	// Ruby's canonical filter_map example: keep even values, multiplied.
	compareArrays(t, got["even_times_ten"], []Value{NewInt(20), NewInt(40)})
	compareArrays(t, got["all_kept"], []Value{NewInt(2), NewInt(4), NewInt(6), NewInt(8)})
	// A block that always returns nil drops every element.
	compareArrays(t, got["none_kept"], []Value{})
	compareArrays(t, got["empty"], []Value{})
	// false predicates are dropped; only the truthy (true) result remains, and
	// filter_map keeps the block's return value, not the original element.
	compareArrays(t, got["drops_false"], []Value{NewBool(true)})
	// filter_map does not mutate the receiver.
	compareArrays(t, got["original"], []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})
}

// TestArrayFilterMapDropsVibescriptFalsy documents that filter_map uses
// Vibescript's truthiness model (matching select/reject), so 0, "", and empty
// collections are dropped alongside nil and false. This diverges from Ruby,
// where only nil and false are falsy, but stays internally consistent with the
// other predicate-driven enumerable helpers.
func TestArrayFilterMapDropsVibescriptFalsy(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      [0, 1, "", "x", [], [9], 2].filter_map do |v|
        v
      end
    end
    `)
	result := callFunc(t, script, "run", nil)
	compareArrays(t, result, []Value{NewInt(1), NewString("x"), NewArray([]Value{NewInt(9)}), NewInt(2)})
}

// arrayFilterMapBuiltin returns the array.filter_map builtin's Go function so a
// test can invoke filter_map directly against a pre-built receiver. Driving the
// builtin this way isolates filter_map's own per-element accounting: a Go-level
// []Value receiver costs no steps and no call-binding memory, so the only quota
// pressure during the call comes from the loop itself rather than from
// materializing the receiver or binding it into a call frame.
func arrayFilterMapBuiltin(t *testing.T) BuiltinFunc {
	t.Helper()
	member, err := arrayMember(NewArray(nil), "filter_map")
	if err != nil {
		t.Fatalf("arrayMember(filter_map): %v", err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("filter_map member is not a builtin: %v", member.Kind())
	}
	return builtin.Fn
}

// emptyBlockValue builds a block with no parameters and an empty body, the
// degenerate case (`do |v| end`) that evaluates no statements and therefore
// charges no steps through runner.call.
func emptyBlockValue() Value {
	return NewBlock(nil, nil, newEnv(nil))
}

// TestArrayFilterMapEmptyBlockParticipatesInStepQuota guards against an empty
// block body letting filter_map traverse the whole receiver without charging the
// step quota. runner.call only charges steps for the statements it evaluates,
// and an empty block evaluates none, so filter_map must charge a step per
// element itself.
//
// The builtin is driven directly against a pre-built Go-level receiver under a
// generous memory quota so the only thing that can trip the limit is the
// per-element step. A script-level test cannot isolate this: passing a large
// receiver trips the memory quota while binding the call frame (which also
// surfaces as runtimeErrorTypeLimit), masking a missing per-element step. The
// assertion below targets errStepQuotaExceeded specifically so a memory-quota
// trip cannot satisfy it.
func TestArrayFilterMapEmptyBlockParticipatesInStepQuota(t *testing.T) {
	t.Parallel()
	const receiverSize = 1000
	const stepQuota = 40
	fn := arrayFilterMapBuiltin(t)
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
}

// TestArrayFilterMapEmptyBlockHonorsCancellation guards against an empty block
// body letting filter_map ignore context cancellation. The per-element step also
// observes the canceled context, so the call must abort rather than run to
// completion.
//
// As with the step-quota test, the builtin is driven directly: a script-level
// canceled-context test trips on the very first step charged while evaluating
// the function body, before filter_map's loop runs, so it would still pass even
// if filter_map never checked cancellation. Driving the builtin against a
// pre-built receiver makes the loop the only place a cancellation can be
// observed.
func TestArrayFilterMapEmptyBlockHonorsCancellation(t *testing.T) {
	t.Parallel()
	const receiverSize = 1000
	fn := arrayFilterMapBuiltin(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := &Execution{
		ctx:         ctx,
		quota:       1 << 30,
		memoryQuota: 8 << 30,
	}
	_, err := fn(exec, largeIntArray(receiverSize), nil, nil, emptyBlockValue())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("filter_map with empty block under canceled context = %v, want context.Canceled", err)
	}
	if exec.steps >= receiverSize {
		t.Fatalf("steps = %d, want the loop to abort early on cancellation", exec.steps)
	}
}

// TestArrayFilterMapDoesNotPreallocateReceiverSize guards against filter_map
// reserving a backing array sized to the whole receiver before any memory-quota
// check can see it. The result slice is not reachable from the execution's
// memory roots while it is built, so reserving len(receiver) would let a sparse
// result allocate and then drop transient storage that the post-call check never
// charges. The receiver here is well above the small fixed seed capacity, yet a
// sparse result must keep its backing array proportional to the elements kept,
// not to the receiver length.
func TestArrayFilterMapDoesNotPreallocateReceiverSize(t *testing.T) {
	t.Parallel()
	const receiverSize = 1000
	cfg := Config{MemoryQuotaBytes: 8 * 1024 * 1024}
	script := compileScriptWithConfig(t, cfg, `def run(values); values.filter_map do |v| nil end; end`)
	result := callFunc(t, script, "run", []Value{largeIntArray(receiverSize)})
	if result.Kind() != KindArray {
		t.Fatalf("expected array, got %v", result.Kind())
	}
	arr := result.Array()
	if len(arr) != 0 {
		t.Fatalf("expected empty result, got %d elements", len(arr))
	}
	if cap(arr) >= receiverSize {
		t.Fatalf("sparse result reserved oversized backing array: cap=%d, want well below %d", cap(arr), receiverSize)
	}
}

// freshArrayBlockValue builds a block whose body is a single array literal of
// `width` integer elements. Each invocation evaluates the literal anew, so every
// call allocates a fresh slice backing (a distinct pointer the memory estimator
// counts separately) rather than reusing one shared backing. The result is a
// non-empty array, which is truthy, so filter_map keeps every one.
func freshArrayBlockValue(width int) Value {
	pos := Position{Line: 1, Column: 1}
	elements := make([]Expression, width)
	for i := range elements {
		elements[i] = &IntegerLiteral{Value: int64(i), Position: pos}
	}
	body := []Statement{&ExprStmt{Expr: &ArrayLiteral{Elements: elements, Position: pos}, Position: pos}}
	return NewBlock(nil, body, newEnv(nil))
}

// TestArrayFilterMapChargesAccumulatedResultsDuringIteration guards against the
// accumulating result array escaping the memory quota while the loop runs. The
// out slice is local, so it is invisible to step()'s slow-path checkMemory() and
// to the post-call checkMemoryWith(result); a block that allocates a fresh large
// value per element could therefore pile up far beyond MemoryQuotaBytes before
// the post-call check ever observed the returned array. filter_map must charge
// the accumulating result on every kept element so the limit trips mid-loop.
//
// The builtin is driven directly against a small Go-level receiver so the
// receiver itself costs no steps and no call-binding memory: the receiver fits
// comfortably under the quota, and the only thing that can trip the limit is the
// results the block produces. Asserting the loop aborts early (steps well below
// the receiver length) proves the per-element check caught the growth rather than
// the post-call check, which would only fire after the whole receiver had been
// traversed.
func TestArrayFilterMapChargesAccumulatedResultsDuringIteration(t *testing.T) {
	t.Parallel()
	const receiverSize = 4096
	const resultWidth = 256
	fn := arrayFilterMapBuiltin(t)

	// Size the quota so a handful of kept results fit but accumulating all of
	// them does not. One kept result is an array of resultWidth ints, so cap the
	// quota at a small multiple of that single result's footprint.
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

// sharedArrayBlockValue builds a block that returns the same array value on every
// invocation. Because each call yields a value backed by the identical slice, the
// memory estimator must deduplicate the shared backing across kept elements; the
// post-call check counts that backing once, so the incremental per-element charge
// must too or it would reject a result the post-call check would have accepted.
func sharedArrayBlockValue(shared Value) Value {
	pos := Position{Line: 1, Column: 1}
	env := newEnv(nil)
	env.Define("shared", shared)
	body := []Statement{&ExprStmt{Expr: &Identifier{Name: "shared", Position: pos}, Position: pos}}
	return NewBlock(nil, body, env)
}

// TestArrayFilterMapDoesNotOverchargeAliasedResults guards the incremental
// per-element memory charge against double-counting a backing that every kept
// element shares. The block returns the same array on every call, so the
// post-call checkMemoryWith(result) counts that single backing once. Sizing the
// quota just above the post-call estimate proves the incremental charge
// deduplicates the shared backing exactly as the post-call check does: if it
// re-counted the backing per element it would trip the quota and reject a result
// the post-call check accepts.
func TestArrayFilterMapDoesNotOverchargeAliasedResults(t *testing.T) {
	t.Parallel()
	const receiverSize = 64
	const sharedWidth = 32
	fn := arrayFilterMapBuiltin(t)

	shared := freshIntArrayValue(sharedWidth)
	sharedPayload := newMemoryEstimator().value(shared)
	// The correct charge counts the shared backing once plus the result array's
	// per-element slots; an over-counting charge would count the shared backing
	// once per kept element. Size the quota generously above the correct total but
	// far below the over-counted total so only a deduplicating charge fits.
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
		t.Fatalf("filter_map over aliased results = %v, want success (incremental charge must dedup the shared backing)", err)
	}
	if got.Kind() != KindArray || len(got.Array()) != receiverSize {
		t.Fatalf("filter_map kept %v, want %d aliased elements", got, receiverSize)
	}
}

// TestArrayFilterMapCountsDistinctBackings guards the incremental per-element
// memory charge against under-counting. Every kept element is a freshly allocated
// array with its own backing, so the charger must accumulate each element's
// payload; if it walked only the first element (or otherwise dropped later
// payloads) a dense result of distinct large values would slip past the quota.
// The quota is sized to admit a few kept results but not all of them, so the loop
// must trip before traversing the whole receiver.
func TestArrayFilterMapCountsDistinctBackings(t *testing.T) {
	t.Parallel()
	const receiverSize = 4096
	const resultWidth = 256
	fn := arrayFilterMapBuiltin(t)

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

// TestArrayFilterMapCountsLiveCallRoots guards the incremental per-element memory
// charge against ignoring the builtin's live call roots. While filter_map runs,
// its receiver and block are held on the Go call stack, so they are invisible to
// estimateMemoryUsageBase even though the pre-call checkCallMemoryRoots already
// charged them. If the accumulator seeded its baseline from the base alone, a
// receiver that already nears the quota would leave room for the loop to
// accumulate a result that pushes total live memory over the limit, and the
// breach would only surface at the post-call check — which never runs when the
// builtin is driven directly. Seeding the baseline with the live receiver makes
// the loop trip mid-iteration instead.
//
// The quota is sized to comfortably hold the receiver alone (so seeding it does
// not reject before the loop even starts) but to leave room for only a few kept
// results on top of it. With the receiver counted, the loop must abort well
// before traversing the whole receiver; without it, the few-result headroom would
// be measured against a near-empty baseline and the call would run to completion.
func TestArrayFilterMapCountsLiveCallRoots(t *testing.T) {
	t.Parallel()
	const receiverSize = 4096
	const resultWidth = 16
	fn := arrayFilterMapBuiltin(t)

	receiver := largeIntArray(receiverSize)
	receiverFootprint := newMemoryEstimator().value(receiver)
	oneResult := newMemoryEstimator().value(freshIntArrayValue(resultWidth))

	// Hold the receiver plus a few results, but far fewer than the receiver has
	// elements. Without the receiver in the baseline the same headroom would admit
	// hundreds of results, letting the loop keep every element and succeed.
	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 30,
		memoryQuota: receiverFootprint + oneResult*8,
	}
	_, err := fn(exec, receiver, nil, nil, freshArrayBlockValue(resultWidth))
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps >= receiverSize {
		t.Fatalf("steps = %d, want the loop to trip the memory quota before traversing the whole receiver (%d) — the live receiver must count toward the accumulator baseline", exec.steps, receiverSize)
	}
}

// constantTruthyBlockValue builds a block whose body is a single truthy integer
// literal, so every invocation keeps its element while allocating nothing. Driving
// filter_map with it isolates the per-element memory charge: the only per-element
// work is the charger walking the kept result.
func constantTruthyBlockValue() Value {
	pos := Position{Line: 1, Column: 1}
	body := []Statement{&ExprStmt{Expr: &IntegerLiteral{Value: 1, Position: pos}, Position: pos}}
	return NewBlock(nil, body, newEnv(nil))
}

// TestArrayFilterMapDenseResultStaysLinear guards against the per-element memory
// charge re-walking the whole accumulated result on every append, which made a
// dense filter_map quadratic in the number of kept elements. The block keeps
// every element, so the old per-element checkMemoryWith(NewArray(out)) walked all
// of out on each append for O(n^2) element walks; the incremental charge walks
// each kept element exactly once for O(n) total. At this receiver size the
// quadratic walk would take on the order of n^2/2 ~= 1.1e10 element visits
// (measured at ~19s before this fix) while the linear charge completes in
// milliseconds.
//
// The builtin is driven directly against a Go-level receiver under generous step
// and memory quotas so the only per-element cost measured is the charger itself,
// not interpreter steps or call-binding memory.
func TestArrayFilterMapDenseResultStaysLinear(t *testing.T) {
	t.Parallel()
	const receiverSize = 150000
	fn := arrayFilterMapBuiltin(t)
	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 30,
		memoryQuota: 256 * 1024 * 1024,
	}
	got, err := fn(exec, largeIntArray(receiverSize), nil, nil, constantTruthyBlockValue())
	if err != nil {
		t.Fatalf("dense filter_map = %v, want success", err)
	}
	if got.Kind() != KindArray {
		t.Fatalf("expected array, got %v", got.Kind())
	}
	if n := len(got.Array()); n != receiverSize {
		t.Fatalf("filter_map kept %d elements, want %d", n, receiverSize)
	}
}

// freshIntArrayValue returns an array Value of `width` integer elements, matching
// the runtime value a freshArrayBlockValue invocation produces so a test can size
// the memory quota against one kept result.
func freshIntArrayValue(width int) Value {
	values := make([]Value, width)
	for i := range width {
		values[i] = NewInt(int64(i))
	}
	return NewArray(values)
}

// TestArrayEnumerableSparseResultsAreRightSized guards against the filtering
// helpers retaining a backing array sized to the whole receiver when the result
// is sparse. reject/take_while/grep all preallocate capacity equal to the
// receiver, so a result that drops most elements must be trimmed to avoid
// charging the caller's memory quota for storage it cannot reach.
func TestArrayEnumerableSparseResultsAreRightSized(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "reject",
			source: `def run(values); values.reject do |v| true end; end`,
		},
		{
			name:   "take_while",
			source: `def run(values); values.take_while do |v| false end; end`,
		},
		{
			name:   "filter_map",
			source: `def run(values); values.filter_map do |v| nil end; end`,
		},
		{
			name:   "grep",
			source: `def run(values); values.grep(-1); end`,
		},
		{
			name:   "grep_v",
			source: `def run(values); values.grep_v(0..100000); end`,
		},
	}

	const receiverSize = 1000
	// The receiver is large enough that retaining its backing array would be a
	// real cost, so the quota is raised well above the default to ensure the
	// test exercises trimming rather than the quota tripping on the input.
	cfg := Config{MemoryQuotaBytes: 8 * 1024 * 1024}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, cfg, tc.source)
			result := callFunc(t, script, "run", []Value{largeIntArray(receiverSize)})
			if result.Kind() != KindArray {
				t.Fatalf("expected array, got %v", result.Kind())
			}
			arr := result.Array()
			if len(arr) != 0 {
				t.Fatalf("expected empty result, got %d elements", len(arr))
			}
			if cap(arr) >= receiverSize {
				t.Fatalf("sparse result retained oversized backing array: cap=%d, want trimmed below %d", cap(arr), receiverSize)
			}
		})
	}
}

func TestArrayEnumerableHelperErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "reject without block",
			source: `def run(); [1, 2].reject; end`,
			want:   "array.reject requires a block",
		},
		{
			name:   "reject with arguments",
			source: `def run(); [1, 2].reject(1) do |n| n end; end`,
			want:   "array.reject does not take arguments",
		},
		{
			name:   "take_while without block",
			source: `def run(); [1, 2].take_while; end`,
			want:   "array.take_while requires a block",
		},
		{
			name:   "take_while with arguments",
			source: `def run(); [1, 2].take_while(1) do |n| n end; end`,
			want:   "array.take_while does not take arguments",
		},
		{
			name:   "drop_while without block",
			source: `def run(); [1, 2].drop_while; end`,
			want:   "array.drop_while requires a block",
		},
		{
			name:   "drop_while with arguments",
			source: `def run(); [1, 2].drop_while(1) do |n| n end; end`,
			want:   "array.drop_while does not take arguments",
		},
		{
			name:   "filter_map without block",
			source: `def run(); [1, 2].filter_map; end`,
			want:   "array.filter_map requires a block",
		},
		{
			name:   "filter_map with arguments",
			source: `def run(); [1, 2].filter_map(1) do |n| n end; end`,
			want:   "array.filter_map does not take arguments",
		},
		{
			name:   "filter_map with keyword arguments",
			source: `def run(); [1, 2].filter_map(extra: true) do |n| n end; end`,
			want:   "array.filter_map does not take keyword arguments",
		},
		{
			name:   "grep without pattern",
			source: `def run(); [1, 2].grep; end`,
			want:   "array.grep expects exactly one pattern argument",
		},
		{
			name:   "grep with extra arguments",
			source: `def run(); [1, 2].grep(1, 2); end`,
			want:   "array.grep expects exactly one pattern argument",
		},
		{
			name:   "grep_v without pattern",
			source: `def run(); [1, 2].grep_v; end`,
			want:   "array.grep_v expects exactly one pattern argument",
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

func TestArrayEnumerableHelperBlockErrorsPropagate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "reject block error",
			source: `def run(); [1, 2].reject do |n| n.frobnicate end; end`,
		},
		{
			name:   "take_while block error",
			source: `def run(); [1, 2].take_while do |n| n.frobnicate end; end`,
		},
		{
			name:   "drop_while block error",
			source: `def run(); [1, 2].drop_while do |n| n.frobnicate end; end`,
		},
		{
			name:   "filter_map block error",
			source: `def run(); [1, 2].filter_map do |n| n.frobnicate end; end`,
		},
		{
			name:   "grep transform block error",
			source: `def run(); [1, 2].grep(1..2) do |n| n.frobnicate end; end`,
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

func TestArrayEnumerableHelpersParticipateInStepQuota(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "reject",
			source: `def run(values); values.reject do |v| v < 0 end; end`,
		},
		{
			name:   "take_while",
			source: `def run(values); values.take_while do |v| v >= 0 end; end`,
		},
		{
			// Predicate stays true across the whole array so the block runs
			// for every element and actually trips the step quota; a predicate
			// that is immediately false would stop after one iteration.
			name:   "drop_while",
			source: `def run(values); values.drop_while do |v| v >= 0 end; end`,
		},
		{
			name:   "filter_map",
			source: `def run(values); values.filter_map do |v| v end; end`,
		},
		{
			name:   "grep transform block",
			source: `def run(values); values.grep(0..100000) do |v| v end; end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: 40}, tc.source)
			requireCallRuntimeErrorType(t, script, "run", []Value{largeIntArray(1000)}, CallOptions{}, runtimeErrorTypeLimit)
		})
	}
}

func TestArrayEnumerableHelpersHonorCancellation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "reject",
			source: `def run(); [3, 1, 2].reject do |v| v < 0 end; end`,
		},
		{
			name:   "take_while",
			source: `def run(); [3, 1, 2].take_while do |v| v >= 0 end; end`,
		},
		{
			name:   "drop_while",
			source: `def run(); [3, 1, 2].drop_while do |v| v < 0 end; end`,
		},
		{
			name:   "filter_map",
			source: `def run(); [3, 1, 2].filter_map do |v| v end; end`,
		},
		{
			name:   "grep transform block",
			source: `def run(); [3, 1, 2].grep(0..9) do |v| v end; end`,
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
