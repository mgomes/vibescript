package runtime

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

// largeHashReceiver builds a hash with count string-keyed integer entries for
// the containment tests. The entries live only in the returned value, so a bare
// Execution can drive a transform builtin against it without the receiver
// counting toward the base memory estimate.
func largeHashReceiver(count int) Value {
	entries := make(map[string]Value, count)
	for i := range count {
		entries["k"+strconv.Itoa(i)] = NewInt(int64(i))
	}
	return NewHash(entries)
}

// callHashMember resolves a hash member builtin and invokes it directly so the
// containment tests can supply a controlled Execution. The builtins are pure
// functions of (exec, receiver, args, kwargs, block), mirroring how the
// interpreter dispatches them.
func callHashMember(t *testing.T, exec *Execution, receiver Value, name string, args []Value, block Value) (Value, error) {
	t.Helper()
	member, err := hashMember(receiver, name)
	if err != nil {
		t.Fatalf("hashMember(%s): %v", name, err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("hash member %s is not a builtin", name)
	}
	return builtin.Fn(exec, receiver, args, nil, block)
}

func TestHashBlocklessTransformTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// Each blockless transform projects the output map it would materialize and
	// rejects the call before reserving a backing map that dwarfs the quota. The
	// receiver holds far more entries than the tiny quota can hold, so the
	// up-front projected check fails rather than the statement-level check
	// catching the allocation after the fact.
	const count = 100_000
	const quota = 8 * 1024

	tests := []struct {
		name string
		args []Value
	}{
		{name: "compact"},
		{name: "except"},
		{name: "merge", args: []Value{largeHashReceiver(count)}},
		{name: "replace", args: []Value{largeHashReceiver(count)}},
		{name: "slice", args: hashSymbolKeys(count)},
		{name: "store", args: []Value{NewSymbol("extra"), NewInt(1)}},
		{name: "remap_keys", args: []Value{NewHash(map[string]Value{"k0": NewSymbol("renamed")})}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			receiver := largeHashReceiver(count)
			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
			_, err := callHashMember(t, exec, receiver, tc.name, tc.args, NewNil())
			requireErrorIs(t, err, errMemoryQuotaExceeded)
		})
	}
}

func TestHashBlocklessTransformHonorsStepQuota(t *testing.T) {
	t.Parallel()

	// Walking a large hash charges a step per entry so a transform stops on the
	// step limit even when the memory quota is ample enough for the result.
	const count = 5_000
	const stepQuota = 100

	tests := []struct {
		name string
		args []Value
	}{
		{name: "compact"},
		{name: "except"},
		{name: "merge", args: []Value{largeHashReceiver(count)}},
		{name: "slice", args: hashSymbolKeys(count)},
		{name: "remap_keys", args: []Value{NewHash(map[string]Value{})}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			receiver := largeHashReceiver(count)
			exec := &Execution{ctx: context.Background(), quota: stepQuota, memoryQuota: 64 << 20}
			_, err := callHashMember(t, exec, receiver, tc.name, tc.args, NewNil())
			requireErrorIs(t, err, errStepQuotaExceeded)
		})
	}
}

func TestHashBlocklessTransformHonorsCancellation(t *testing.T) {
	t.Parallel()

	// step() polls cancellation on its first invocation, so a canceled context
	// aborts the walk before any entries are copied. A small receiver is enough.
	receiver := largeHashReceiver(8)

	tests := []struct {
		name string
		args []Value
	}{
		{name: "compact"},
		{name: "except"},
		{name: "merge", args: []Value{largeHashReceiver(8)}},
		{name: "slice", args: hashSymbolKeys(8)},
		{name: "remap_keys", args: []Value{NewHash(map[string]Value{})}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			exec := &Execution{ctx: ctx, quota: 1 << 30, memoryQuota: 0}
			_, err := callHashMember(t, exec, receiver, tc.name, tc.args, NewNil())
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s under canceled context = %v, want context.Canceled", tc.name, err)
			}
		})
	}
}

// hashSymbolKeys returns count symbol keys matching the entries built by
// largeHashReceiver, for driving slice with many candidate keys.
func hashSymbolKeys(count int) []Value {
	keys := make([]Value, count)
	for i := range count {
		keys[i] = NewSymbol("k" + strconv.Itoa(i))
	}
	return keys
}

func TestHashTransformProjectionCountsLiveCallRoots(t *testing.T) {
	t.Parallel()

	// A transform invoked on an ephemeral receiver holds the input hash alive as a
	// call root while it builds the output map, so peak memory is the input plus
	// the new map. The projected check must count the receiver root or it
	// under-reports the peak and admits a transform that doubles the live
	// footprint. Here the quota fits a single materialized copy of the receiver
	// but not the input held live alongside the output, so compact (which keeps
	// every non-nil entry) must be rejected up front.
	const count = 20_000
	receiver := largeHashReceiver(count)

	// Measure the live footprint of the receiver as a call root and the structural
	// footprint of the output map compact would build, then set the quota so the
	// output alone fits but the input held live alongside it does not. The
	// pre-fix projection counted only the output structure and would have passed.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	liveWithRoot := probe.estimateMemoryUsageForCallRoots(receiver, nil, nil, NewNil())
	outputStructure := estimatedValueBytes + estimatedMapBaseBytes +
		count*(estimatedMapEntryBytes+estimatedStringHeaderBytes+estimatedValueBytes)
	if liveWithRoot <= outputStructure {
		t.Fatalf("test setup expects the live input (%d) to exceed the output structure (%d)", liveWithRoot, outputStructure)
	}

	// Above the output structure (old projection passes) but below input+output
	// (new projection rejects).
	quota := outputStructure + (liveWithRoot-outputStructure)/2

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "compact", nil, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashSliceProjectionBoundsByOutputNotArgCount complements the slice case in
// TestHashBlocklessTransformTripsMemoryQuota (which guards the P1 finding on PR
// #776 that slice reserved make(map, len(args)) before any projected check). It
// pins the other side of the fix: the projected bound is min(len(args),
// len(entries)), not len(args). A tiny receiver with a huge candidate list must
// still fit a quota sized only to the receiver's entries and return the correct
// slice, so the bound can never be over-tightened to reject valid small results.
func TestHashSliceProjectionBoundsByOutputNotArgCount(t *testing.T) {
	t.Parallel()

	receiver := largeHashReceiver(2)
	const argCount = 100_000
	args := hashSymbolKeys(argCount)

	// A quota that fits the live receiver plus an output map of every receiver
	// entry, sized to what slice actually needs.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(receiver, args, nil, NewNil())
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	outputStructure := estimatedValueBytes + estimatedMapBaseBytes + len(receiver.Hash())*perEntry
	quota := liveWithRoots + outputStructure

	// Sanity: a map projected at len(args) would dwarf this quota, so admitting the
	// call proves the projection is bounded by the receiver, not the candidate list.
	argSizedStructure := estimatedValueBytes + estimatedMapBaseBytes + argCount*perEntry
	if argSizedStructure <= quota {
		t.Fatalf("test setup expects an arg-sized map (%d) to exceed the quota (%d)", argSizedStructure, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "slice", args, NewNil())
	if err != nil {
		t.Fatalf("slice with a large candidate list under an output-sized quota = %v, want success", err)
	}
	if got.Kind() != KindHash {
		t.Fatalf("slice returned %v, want hash", got.Kind())
	}
	want := receiver.Hash()
	if len(got.Hash()) != len(want) {
		t.Fatalf("slice produced %d entries, want %d", len(got.Hash()), len(want))
	}
	for key, value := range want {
		if got.Hash()[key].String() != value.String() {
			t.Fatalf("slice entry %q = %v, want %v", key, got.Hash()[key], value)
		}
	}
}

func TestHashMergeProjectionCountsUnionNotSum(t *testing.T) {
	t.Parallel()

	// When a merge argument overlaps the receiver, the output map only needs space
	// for the distinct union of keys, not the sum of every input length. Merging a
	// hash with itself produces a result the same size as the receiver, so a quota
	// that fits the receiver plus its copied result must let the merge proceed.
	// The pre-fix projection summed len(base)+len(arg) and would have rejected
	// this self-overlay even though it stays within the sandbox limit.
	const count = 10_000
	receiver := largeHashReceiver(count)

	// The union of receiver.merge(receiver) is exactly the receiver's keys, so the
	// real output is count entries. Set the quota to fit the live receiver plus an
	// output map of count entries, which is what the operation actually needs.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(receiver, []Value{receiver}, nil, NewNil())
	outputStructure := estimatedValueBytes + estimatedMapBaseBytes +
		count*(estimatedMapEntryBytes+estimatedStringHeaderBytes+estimatedValueBytes)
	quota := liveWithRoots + outputStructure

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "merge", []Value{receiver}, NewNil())
	if err != nil {
		t.Fatalf("merge(self) under union-sized quota = %v, want success", err)
	}
	if got.Kind() != KindHash {
		t.Fatalf("merge returned %v, want hash", got.Kind())
	}
	if len(got.Hash()) != count {
		t.Fatalf("merge(self) produced %d entries, want %d", len(got.Hash()), count)
	}

	// Sanity: the discarded sum-based projection (len(base)+len(arg) = 2*count)
	// would not fit this quota, confirming the test exercises the union fix.
	sumProjection := estimatedValueBytes + estimatedMapBaseBytes +
		2*count*(estimatedMapEntryBytes+estimatedStringHeaderBytes+estimatedValueBytes)
	if liveWithRoots+sumProjection <= quota {
		t.Fatalf("test setup expects the sum-based projection (%d) to exceed the quota (%d)", liveWithRoots+sumProjection, quota)
	}
}

func TestHashMergeMultiArgOverlapStaysWithinQuota(t *testing.T) {
	t.Parallel()

	// A merge whose arguments all duplicate the receiver collapses to the
	// receiver's keys, so it fits a union-sized quota. The loose upper bound
	// (len(base) + sum of argument lengths) exceeds that quota, forcing the exact
	// multi-argument union count, which must deduplicate the repeated keys and
	// admit the merge. This exercises the two-phase containment path: the loose
	// precheck fails, then the capped union count brings the projection back
	// within budget without growing an unbounded tracking set.
	const count = 4_000
	receiver := largeHashReceiver(count)
	args := []Value{receiver, receiver, receiver}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(receiver, args, nil, NewNil())
	outputStructure := estimatedValueBytes + estimatedMapBaseBytes +
		count*(estimatedMapEntryBytes+estimatedStringHeaderBytes+estimatedValueBytes)
	quota := liveWithRoots + outputStructure

	// Sanity: the loose upper bound sums every argument length, so it exceeds the
	// union-sized quota and the exact count path must run for this merge to pass.
	looseProjection := estimatedValueBytes + estimatedMapBaseBytes +
		(count+len(args)*count)*(estimatedMapEntryBytes+estimatedStringHeaderBytes+estimatedValueBytes)
	if liveWithRoots+looseProjection <= quota {
		t.Fatalf("test setup expects the loose projection (%d) to exceed the quota (%d)", liveWithRoots+looseProjection, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "merge", args, NewNil())
	if err != nil {
		t.Fatalf("merge with overlapping args under union-sized quota = %v, want success", err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != count {
		t.Fatalf("merge produced %v with %d entries, want a hash with %d", got.Kind(), len(got.Hash()), count)
	}
}

func TestHashMergeMultiArgDistinctKeysTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// A merge whose multiple arguments contribute many distinct new keys exceeds a
	// tiny quota and must be rejected up front. The containment must reject it
	// without first allocating a deduplication set sized to the rejected union, so
	// the merge fails fast on the projected check rather than after building a
	// large tracking table.
	const count = 50_000
	const quota = 8 * 1024
	receiver := largeHashReceiver(8)

	first := make(map[string]Value, count)
	second := make(map[string]Value, count)
	for i := range count {
		first["f"+strconv.Itoa(i)] = NewInt(int64(i))
		second["s"+strconv.Itoa(i)] = NewInt(int64(i))
	}
	args := []Value{NewHash(first), NewHash(second)}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "merge", args, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

func TestMergedKeyCount(t *testing.T) {
	t.Parallel()

	base := map[string]Value{"a": NewInt(1), "b": NewInt(2)}

	tests := []struct {
		name  string
		args  []Value
		limit int
		want  int
	}{
		{name: "no args", limit: 100, want: 2},
		{name: "self overlay", args: []Value{NewHash(base)}, limit: 100, want: 2},
		{name: "all new keys", args: []Value{NewHash(map[string]Value{"c": NewInt(3), "d": NewInt(4)})}, limit: 100, want: 4},
		{name: "partial overlap", args: []Value{NewHash(map[string]Value{"b": NewInt(9), "c": NewInt(3)})}, limit: 100, want: 3},
		{
			name: "duplicate new key across args",
			args: []Value{
				NewHash(map[string]Value{"c": NewInt(3)}),
				NewHash(map[string]Value{"c": NewInt(4)}),
			},
			limit: 100,
			want:  3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exec := &Execution{ctx: context.Background()}
			got, err := mergedKeyCount(exec, base, tc.args, tc.limit)
			if err != nil {
				t.Fatalf("mergedKeyCount(%s) error = %v, want nil", tc.name, err)
			}
			if got != tc.want {
				t.Fatalf("mergedKeyCount(%s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestMergedKeyCountStopsAtLimit verifies the union counter never grows its
// deduplication set past the quota-derived budget. With many distinct new keys
// spread across multiple arguments the exact union would be large, but once the
// running count exceeds the limit the helper returns limit+1 without continuing
// to track keys, so a doomed over-quota merge cannot allocate a tracking table
// sized to the rejected result. This guards the P1 finding on PR #776.
func TestMergedKeyCountStopsAtLimit(t *testing.T) {
	t.Parallel()

	base := map[string]Value{"a": NewInt(1)}
	// Two arguments so the multi-argument deduplication path runs; the keys are
	// disjoint from base and from each other, so the true union is 1+500+500.
	first := make(map[string]Value, 500)
	second := make(map[string]Value, 500)
	for i := range 500 {
		first["f"+strconv.Itoa(i)] = NewInt(int64(i))
		second["s"+strconv.Itoa(i)] = NewInt(int64(i))
	}
	args := []Value{NewHash(first), NewHash(second)}

	const limit = 10
	exec := &Execution{ctx: context.Background()}
	got, err := mergedKeyCount(exec, base, args, limit)
	if err != nil {
		t.Fatalf("mergedKeyCount error = %v, want nil", err)
	}
	if got <= limit {
		t.Fatalf("mergedKeyCount returned %d, want a value greater than the limit %d", got, limit)
	}
	if got > limit+1 {
		t.Fatalf("mergedKeyCount returned %d, want it to stop at limit+1 (%d) rather than counting the full union", got, limit+1)
	}
}

// TestMergedKeyCountSingleArgNeedsNoTrackingSet verifies the single-argument path
// counts the exact union against the receiver alone without a deduplication set,
// even when the result is rejected. A single argument can never repeat a key
// within itself, so no tracking table is required regardless of size.
func TestMergedKeyCountSingleArgNeedsNoTrackingSet(t *testing.T) {
	t.Parallel()

	base := map[string]Value{"a": NewInt(1)}
	arg := make(map[string]Value, 100)
	for i := range 100 {
		arg["x"+strconv.Itoa(i)] = NewInt(int64(i))
	}

	exec := &Execution{ctx: context.Background()}
	// A generous limit yields the exact union (base plus the 100 disjoint keys).
	got, err := mergedKeyCount(exec, base, []Value{NewHash(arg)}, 1_000)
	if err != nil {
		t.Fatalf("mergedKeyCount(single arg) error = %v, want nil", err)
	}
	if got != 101 {
		t.Fatalf("mergedKeyCount(single arg) = %d, want 101", got)
	}
	// A tight limit stops early without tracking, reporting an over-budget count.
	got, err = mergedKeyCount(exec, base, []Value{NewHash(arg)}, 5)
	if err != nil {
		t.Fatalf("mergedKeyCount(single arg, limit 5) error = %v, want nil", err)
	}
	if got <= 5 {
		t.Fatalf("mergedKeyCount(single arg, limit 5) = %d, want it to exceed the limit", got)
	}
}

// TestMergedKeyCountChargesStepsPerKey verifies the union counter charges a step
// for every key it examines, so counting a large overlapping merge is itself
// CPU-bounded by the step quota. With a high entry limit the count would otherwise
// scan the whole argument before any check ran; here the step quota trips mid-walk
// and the helper propagates the error instead of finishing the O(n) scan. This
// guards the P2 finding on PR #776, where the exact-count preflight could burn
// O(n) CPU after the step quota was already exhausted.
func TestMergedKeyCountChargesStepsPerKey(t *testing.T) {
	t.Parallel()

	base := map[string]Value{"a": NewInt(1)}
	// A single overlapping argument: every key is already in base, so the union is
	// tiny (the loose projection would even fit), yet each key must still cost a
	// step. A high limit ensures the limit-based early exit cannot mask the walk.
	arg := make(map[string]Value, 1_000)
	for i := range 1_000 {
		arg["k"+strconv.Itoa(i)] = NewInt(int64(i))
	}

	const quota = 10
	exec := &Execution{ctx: context.Background(), quota: quota}
	_, err := mergedKeyCount(exec, base, []Value{NewHash(arg)}, math.MaxInt)
	requireErrorIs(t, err, errStepQuotaExceeded)
	if exec.steps > quota+1 {
		t.Fatalf("mergedKeyCount took %d steps, want it to stop near the quota %d", exec.steps, quota)
	}
}

// TestMergedKeyCountObservesCancellation verifies the union counter observes a
// canceled context while walking keys, so a sandboxed merge cannot keep scanning
// after cancellation. step checks the context on its first call, so a counter that
// charges a step per key abandons the walk immediately.
func TestMergedKeyCountObservesCancellation(t *testing.T) {
	t.Parallel()

	base := map[string]Value{"a": NewInt(1)}
	arg := make(map[string]Value, 1_000)
	for i := range 1_000 {
		arg["k"+strconv.Itoa(i)] = NewInt(int64(i))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := &Execution{ctx: ctx, quota: 1 << 30}
	_, err := mergedKeyCount(exec, base, []Value{NewHash(arg)}, math.MaxInt)
	requireErrorIs(t, err, context.Canceled)
}

// TestHashMergeUnionCountHonorsStepQuota drives the finding end to end through the
// merge builtin. A small receiver merged with the same large argument twice makes
// the loose upper bound (receiver + both argument lengths) exceed the memory quota
// while the deduplicated call-root projection still fits, so the exact union count
// runs and walks every argument key. That walk must respect the step quota: without
// charging steps in mergedKeyCount the preflight would scan tens of thousands of
// keys before the step quota was observed. The memory quota is sized so the
// per-key step charge trips first, proving the count is CPU-bounded.
func TestHashMergeUnionCountHonorsStepQuota(t *testing.T) {
	t.Parallel()

	const count = 50_000
	argument := largeHashReceiver(count)
	receiver := NewHash(map[string]Value{"z": NewInt(0)})
	// Passing the same argument value twice keeps the deduplicated projection at one
	// copy's size (so the exact-count path is reached) while the loose bound counts
	// both lengths (so it overflows and forces that path).
	args := []Value{argument, argument}

	exec := &Execution{ctx: context.Background(), quota: 100, memoryQuota: 8_000_000}
	_, err := callHashMember(t, exec, receiver, "merge", args, NewNil())
	requireErrorIs(t, err, errStepQuotaExceeded)
}

func TestHashBlockTransformTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// Block-based transforms preflight the output map before reserving it, so a
	// large receiver trips the quota before the first block call. Building the
	// receiver in-script keeps the receiver out of the call arguments while the
	// transform's projected check observes its size.
	const count = 50_000
	source := `def run(n)
  h = {}
  for i in 1..n
    h["k" + i] = i
  end
  h.transform_values { |v| v }
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 256 * 1024}, source)
	requireRunMemoryQuotaError(t, script, []Value{NewInt(count)}, CallOptions{})
}

func TestHashBlockTransformHonorsStepQuota(t *testing.T) {
	t.Parallel()

	// Each block invocation charges a step, so a block-based transform over a
	// large hash stops on the step limit even with ample memory. The receiver is
	// passed in so the tight step quota trips inside the transform's block walk
	// rather than while building the hash.
	source := `def run(values)
  values.transform_values { |v| v }
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 40, MemoryQuotaBytes: 64 << 20}, source)
	requireCallRuntimeErrorType(t, script, "run", []Value{largeHashReceiver(2_000)}, CallOptions{}, runtimeErrorTypeLimit)
}

func TestHashBlockTransformHonorsCancellation(t *testing.T) {
	t.Parallel()

	// A canceled context aborts the block walk: step() polls cancellation on its
	// first invocation, so even a tiny hash observes it.
	source := `def run(values)
  values.select { |k, v| v > 0 }
end`
	script := compileScript(t, source)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	receiver := NewHash(map[string]Value{"a": NewInt(1), "b": NewInt(2), "c": NewInt(3)})
	_, err := script.Call(ctx, "run", []Value{receiver}, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("select under canceled context = %v, want context.Canceled", err)
	}
}

// hashStoreProjectionPerEntry mirrors the per-entry footprint the store
// projection charges, for sizing quotas in the store tests below.
func hashStoreProjectionBytes(t *testing.T, receiver Value, args []Value, entries int) int {
	t.Helper()
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	live := probe.estimateMemoryUsageForCallRoots(receiver, args, nil, NewNil())
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	return live + estimatedValueBytes + estimatedMapBaseBytes + entries*perEntry
}

// TestHashStoreExistingKeyFitsReceiverQuota pins the P2 finding on PR #776: when
// store replaces an existing key, the result keeps len(base) entries, so a quota
// sized to a copy of the receiver must admit the update. The pre-fix projection
// always charged len(base)+1 and would reject this in-place-style replacement
// even though the output stays within the limit.
func TestHashStoreExistingKeyFitsReceiverQuota(t *testing.T) {
	t.Parallel()

	const count = 5_000
	receiver := largeHashReceiver(count)
	args := []Value{NewSymbol("k0"), NewInt(999)}

	// Size the quota to exactly a copy of the receiver (len(base) entries). Storing
	// an existing key must fit; the discarded len(base)+1 projection would not.
	quota := hashStoreProjectionBytes(t, receiver, args, count)
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "store", args, NewNil())
	if err != nil {
		t.Fatalf("store(existing key) under receiver-sized quota = %v, want success", err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != count {
		t.Fatalf("store produced %v with %d entries, want a hash with %d", got.Kind(), len(got.Hash()), count)
	}
	if got.Hash()["k0"].String() != NewInt(999).String() {
		t.Fatalf("store did not replace k0: got %v", got.Hash()["k0"])
	}

	// Sanity: a new key grows the map to len(base)+1, which this quota cannot hold,
	// confirming the quota is tight enough to exercise the existing-key case.
	exec = &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err = callHashMember(t, exec, receiver, "store", []Value{NewSymbol("brand_new"), NewInt(1)}, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashExceptFailsFastOnTinyReceiver pins the P1 finding on PR #776: a tiny
// receiver paired with a huge candidate-key list must be rejected on the output
// bound (len(entries)) before allocating or scanning a set proportional to the
// argument count. Here the receiver's output fits, but the call roots (the huge
// candidate list) blow the quota, so the projected check rejects up front.
func TestHashExceptFailsFastOnTinyReceiver(t *testing.T) {
	t.Parallel()

	receiver := largeHashReceiver(2)
	const argCount = 200_000
	args := hashSymbolKeys(argCount)

	// A quota that fits the receiver and its output but not the candidate list held
	// alive as call roots: the projected check counts the roots and must reject.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	receiverLive := probe.estimateMemoryUsageForCallRoots(receiver, nil, nil, NewNil())
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	quota := receiverLive + estimatedValueBytes + estimatedMapBaseBytes + len(receiver.Hash())*perEntry + 4*1024

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "except", args, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashExceptHonorsStepQuotaOnCandidateScan pins the other half of the P1
// except finding: building the exclusion set charges a step per candidate, so a
// huge candidate list against a tiny receiver stops on the step limit even when
// memory is ample. Before the fix the entire argument scan ran before any step or
// cancellation poll.
func TestHashExceptHonorsStepQuotaOnCandidateScan(t *testing.T) {
	t.Parallel()

	receiver := largeHashReceiver(2)
	args := hashSymbolKeys(50_000)

	const stepQuota = 100
	exec := &Execution{ctx: context.Background(), quota: stepQuota, memoryQuota: 64 << 20}
	_, err := callHashMember(t, exec, receiver, "except", args, NewNil())
	requireErrorIs(t, err, errStepQuotaExceeded)
	if exec.steps > stepQuota+1 {
		t.Fatalf("except scanned %d steps, want it to stop near the quota %d", exec.steps, stepQuota)
	}
}

// TestHashExceptHonorsCancellationOnCandidateScan verifies the candidate scan
// observes a canceled context. step polls cancellation on its first call, so the
// scan aborts before populating the exclusion set even with a huge argument list.
func TestHashExceptHonorsCancellationOnCandidateScan(t *testing.T) {
	t.Parallel()

	receiver := largeHashReceiver(2)
	args := hashSymbolKeys(50_000)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := &Execution{ctx: ctx, quota: 1 << 30, memoryQuota: 0}
	_, err := callHashMember(t, exec, receiver, "except", args, NewNil())
	requireErrorIs(t, err, context.Canceled)
}

// TestHashBuildAccumulatorChargesIncrementally exercises the accumulator that
// charges block-returned values during a hash build. Feeding values whose
// cumulative payload crosses the quota must trip on the insertion that crosses
// it, not after the whole map is assembled, proving the P1 transform_values
// finding is bounded incrementally rather than only by the post-call check.
func TestHashBuildAccumulatorChargesIncrementally(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	exec.root = newEnv(nil)
	// Headroom for several large values but not many: each payload is 4 KiB and the
	// quota above the live baseline admits roughly three before tripping.
	const payloadBytes = 4 * 1024
	base := exec.estimateMemoryUsageBase(newMemoryEstimator())
	exec.memoryQuota = base + estimatedValueBytes + estimatedMapBaseBytes + 3*(payloadBytes+128)

	acc := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())

	var tripped int
	for i := range 100 {
		// Distinct keys and distinct value backings so nothing dedups to zero: each
		// add charges a fresh payload, mirroring a block that returns new heap
		// values per entry.
		key := "k" + strconv.Itoa(i)
		fresh := NewString(strings.Repeat("x", payloadBytes))
		if err := acc.add(key, fresh); err != nil {
			requireErrorIs(t, err, errMemoryQuotaExceeded)
			tripped = i
			break
		}
	}
	if tripped == 0 {
		t.Fatalf("accumulator never tripped the quota, want it to reject mid-build")
	}
	if tripped > 5 {
		t.Fatalf("accumulator tripped after %d inserts, want it to fail fast within a few", tripped)
	}
}

// TestHashTransformValuesIncrementalBlockCharging drives the P1 transform_values
// finding end to end: a block that returns a fresh large string per entry
// accumulates payloads only reachable through the Go-local output map, so without
// incremental charging they would slip past the structural projection until the
// post-call check. The memory quota is sized so the accumulated results exceed it
// while each individual result fits, and the step quota is ample, so the failure
// must come from the per-entry memory accounting.
func TestHashTransformValuesIncrementalBlockCharging(t *testing.T) {
	t.Parallel()

	source := `def run(values)
  values.transform_values { |v| "".ljust(4096, "x") }
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 512 * 1024}, source)
	requireCallRuntimeErrorType(t, script, "run", []Value{largeHashReceiver(2_000)}, CallOptions{}, runtimeErrorTypeLimit)
}

// TestHashMergeConflictBlockIncrementalCharging verifies merge's conflict block
// path is bounded incrementally too: when every key collides, the block returns a
// fresh large value per key, and those results live only in the Go-local output
// map. The union is the receiver's size (so the structural projection passes), but
// the accumulated block results exceed the quota while each fits; an ample step
// quota means the failure comes from the per-entry memory accounting.
func TestHashMergeConflictBlockIncrementalCharging(t *testing.T) {
	t.Parallel()

	const count = 2_000
	receiver := largeHashReceiver(count)
	other := largeHashReceiver(count)

	source := `def run(a, b)
  a.merge(b) { |k, old, new| "".ljust(2048, "z") }
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 1024 * 1024}, source)
	requireCallRuntimeErrorType(t, script, "run", []Value{receiver, other}, CallOptions{}, runtimeErrorTypeLimit)
}

// TestHashBuildAccumulatorRecursiveChargeMatchesEstimator pins the recursive
// decomposition used by deep_transform_keys to the estimator's own measurement.
// Charging a nested structure through the per-node/per-entry accumulator methods
// (as the recursion does, in pre-order with the result slot last) must total
// exactly what newMemoryEstimator().value reports for the same structure, so the
// incremental accounting neither under- nor over-counts the rebuilt footprint.
func TestHashBuildAccumulatorRecursiveChargeMatchesEstimator(t *testing.T) {
	t.Parallel()

	// A structure exercising every node shape: a top map with a leaf, a nested
	// map, and a nested array containing a leaf and a nested map. Leaves are ints
	// so they carry no payload -- in a real deep transform the leaves are shared
	// with the input and contribute nothing new, which int leaves model exactly,
	// letting the per-node decomposition match the estimator's full measurement.
	innerMap := NewHash(map[string]Value{"inner": NewInt(2)})
	arrayInnerMap := NewHash(map[string]Value{"deep": NewInt(3)})
	arr := NewArray([]Value{NewInt(4), arrayInnerMap})
	top := NewHash(map[string]Value{
		"leaf":  NewInt(1),
		"child": innerMap,
		"list":  arr,
	})

	want := newMemoryEstimator().value(top)

	// Drive the accumulator with an empty baseline so the charged total is the
	// structure's own footprint, comparable to the estimator's value().
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 40}
	acc := newRootSeededHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())

	var charge func(Value)
	charge = func(v Value) {
		// Mirror deepTransformKeysWithState: every value charges its header, each
		// map node its base plus a shell per entry, each array node its backing.
		if err := acc.addValueBase(); err != nil {
			t.Fatalf("addValueBase: %v", err)
		}
		switch v.Kind() {
		case KindHash, KindObject:
			if err := acc.addMapBase(); err != nil {
				t.Fatalf("addMapBase: %v", err)
			}
			m := v.Hash()
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			for _, k := range keys {
				if err := acc.addMapEntryShell(k); err != nil {
					t.Fatalf("addMapEntryShell: %v", err)
				}
				charge(m[k])
			}
		case KindArray:
			if err := acc.addArrayBase(len(v.Array())); err != nil {
				t.Fatalf("addArrayBase: %v", err)
			}
			for _, item := range v.Array() {
				charge(item)
			}
		default:
			// Leaf values are shared with the input; only the header (charged
			// above) is new, so their payload adds nothing here.
		}
	}
	charge(top)

	// built holds exactly the charged structure, separate from the live baseline.
	if acc.built != want {
		t.Fatalf("recursive accumulator charged %d bytes, want estimator total %d", acc.built, want)
	}
}

// TestHashTransformKeysIncrementalBlockCharging drives the same P1 finding for
// transform_keys: the block synthesizes a fresh large key per entry, and those
// keys live only in the Go-local output map, so the structural projection cannot
// bound them. Distinct keys keep every entry, so the accumulated key payloads
// exceed the quota while each individual key fits; an ample step quota means the
// failure must come from the per-entry memory accounting.
func TestHashTransformKeysIncrementalBlockCharging(t *testing.T) {
	t.Parallel()

	source := `def run(values)
  values.transform_keys { |k| ("p" + k).ljust(2048, "z") }
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 512 * 1024}, source)
	requireCallRuntimeErrorType(t, script, "run", []Value{largeHashReceiver(2_000)}, CallOptions{}, runtimeErrorTypeLimit)
}

// TestHashDeepTransformKeysTripsMemoryQuota verifies the recursive transform
// charges the rebuilt structure incrementally: rebuilding a wide hash duplicates
// the top map and synthesizes every key, so a quota that fits the live input but
// not the input held alongside its rebuild is rejected during construction. The
// receiver is passed in so building the input is not what trips the quota. Before
// adding accounting, deep_transform_keys built the entire result with no memory
// or step bound before the post-call check.
func TestHashDeepTransformKeysTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	const count = 10_000
	receiver := largeHashReceiver(count)

	// The rebuild duplicates the top map's structure (a fresh entry slot per key).
	// Size the quota to comfortably hold the live receiver but not the receiver plus
	// a full duplicate of its entries, so the rebuild trips part way through.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	receiverLive := probe.estimateMemoryUsageForCallRoots(receiver, nil, nil, NewNil())
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	rebuildStructure := estimatedValueBytes + estimatedMapBaseBytes + count*perEntry
	// Headroom above the live input for only a fraction of the rebuild.
	quota := receiverLive + rebuildStructure/2

	source := `def run(values)
  values.deep_transform_keys { |k| k }
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: quota}, source)
	requireCallRuntimeErrorType(t, script, "run", []Value{receiver}, CallOptions{}, runtimeErrorTypeLimit)
}

// TestHashDeepTransformKeysHonorsStepQuota verifies the recursion charges a step
// per visited key and array element, so a large nested structure stops on the
// step limit even with ample memory.
func TestHashDeepTransformKeysHonorsStepQuota(t *testing.T) {
	t.Parallel()

	source := `def run(values)
  values.deep_transform_keys { |k| k }
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 40, MemoryQuotaBytes: 64 << 20}, source)
	requireCallRuntimeErrorType(t, script, "run", []Value{largeHashReceiver(2_000)}, CallOptions{}, runtimeErrorTypeLimit)
}

// TestHashDeepTransformKeysHonorsCancellation verifies the recursion observes a
// canceled context: step polls cancellation on its first call, so even a tiny
// nested structure aborts before rebuilding it.
func TestHashDeepTransformKeysHonorsCancellation(t *testing.T) {
	t.Parallel()

	source := `def run(values)
  values.deep_transform_keys { |k| k }
end`
	script := compileScript(t, source)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	receiver := NewHash(map[string]Value{"a": NewInt(1), "b": NewInt(2)})
	_, err := script.Call(ctx, "run", []Value{receiver}, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("deep_transform_keys under canceled context = %v, want context.Canceled", err)
	}
}
