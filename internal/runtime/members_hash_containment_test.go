package runtime

import (
	"context"
	"errors"
	"fmt"
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
	// real output is count entries. Set the quota to fit the live receiver, an
	// output map of count entries, and the sorted key scratch buffer merge sorts
	// the argument into -- all of what the operation actually needs.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(receiver, []Value{receiver}, nil, NewNil())
	outputStructure := estimatedValueBytes + estimatedMapBaseBytes +
		count*(estimatedMapEntryBytes+estimatedStringHeaderBytes+estimatedValueBytes)
	scratch := sortedKeyBufferBytes(count)
	quota := liveWithRoots + outputStructure + scratch

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
	// The per-argument sorted key scratch buffer is reused across arguments, so it
	// only sizes to the largest single argument (count keys here). Fold it into the
	// quota alongside the union-sized output map.
	scratch := sortedKeyBufferBytes(count)
	quota := liveWithRoots + outputStructure + scratch

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

func TestHashMergeRejectsWhenScratchExceedsQuota(t *testing.T) {
	t.Parallel()

	// A multi-argument merge whose arguments fully overlap the receiver collapses
	// to the receiver's keys, so its output map fits a quota sized for the union
	// alone. The sorted-key scratch buffer, however, sizes to the largest single
	// argument and is materialized alongside the output. When the quota admits the
	// union output but not the scratch on top of it, the merge must be rejected.
	//
	// Before the fix, maxProjectedHashEntries derived its entry cap from the byte
	// budget WITHOUT the scratch the final projection charges, so the cap admitted
	// the full union; mergedKeyCount then built a deduplication set sized to that
	// union before the scratch-aware projection rejected the merge. That temporary
	// set is exactly the allocation the quota is meant to bound. Subtracting the
	// scratch budget before deriving the cap makes the exact-count ceiling agree
	// with the projection, so the doomed merge stops counting at the real budget.
	const count = 5_000
	receiver := largeHashReceiver(count)
	// Two arguments, both full duplicates of the receiver, force the multi-argument
	// deduplication path while keeping the union exactly the receiver's keys.
	args := []Value{receiver, receiver}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	projectedBase := probe.projectedHashBaseBytes(receiver, args, nil, NewNil())
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	unionOutput := count * perEntry
	scratch := mergeSortScratchBytes(receiver.Hash(), args, false)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heaped scratch buffer, got %d bytes", scratch)
	}

	// Size the quota to fit the union output but fall one byte short of also fitting
	// the scratch buffer, so the scratch is the sole reason the merge is rejected.
	quota := projectedBase + unionOutput + scratch - 1

	// The byte budget left after the union output is consumed is exactly the
	// scratch minus one, so a scratch-blind entry cap would still admit the full
	// union and let mergedKeyCount build a set of count keys.
	scratchBlindCap := (quota - projectedBase) / perEntry
	if scratchBlindCap < count {
		t.Fatalf("test setup expects a scratch-blind cap (%d) of at least the union size (%d)", scratchBlindCap, count)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	if got := exec.maxProjectedHashEntries(scratch, receiver, args, nil, NewNil()); got >= count {
		t.Fatalf("maxProjectedHashEntries with scratch = %d, want it capped below the union size %d so the dedup set stays within the scratch-aware budget", got, count)
	}

	_, err := callHashMember(t, exec, receiver, "merge", args, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestMaxProjectedHashEntriesAgreesWithProjection verifies the entry cap and the
// byte projection agree once a scratch budget is reserved: the cap is the largest
// entry count checkProjectedHashTransformBytes still accepts for the same scratch,
// and one entry past it is rejected. This guards the P1 finding on PR #776, where
// the cap was derived from a byte budget that omitted the scratch the projection
// charged, so the cap admitted entries the projection's real budget could not.
func TestMaxProjectedHashEntriesAgreesWithProjection(t *testing.T) {
	t.Parallel()

	receiver := largeHashReceiver(1_000)
	args := []Value{receiver}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	projectedBase := probe.projectedHashBaseBytes(receiver, args, nil, NewNil())
	scratch := sortedKeyBufferBytes(2_000)
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes

	// Pick a quota that admits some entries beyond the base plus scratch so the cap
	// is a positive number bounded by the byte budget.
	const wantCap = 50
	quota := projectedBase + scratch + wantCap*perEntry
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}

	entryCap := exec.maxProjectedHashEntries(scratch, receiver, args, nil, NewNil())
	if entryCap != wantCap {
		t.Fatalf("maxProjectedHashEntries = %d, want %d", entryCap, wantCap)
	}

	// The projection must accept exactly entryCap entries and reject entryCap+1,
	// proving the two views of the budget share the same scratch reservation.
	if err := exec.checkProjectedHashTransformBytes(entryCap, scratch, receiver, args, nil, NewNil()); err != nil {
		t.Fatalf("checkProjectedHashTransformBytes(%d, scratch) = %v, want it to fit the cap", entryCap, err)
	}
	if err := exec.checkProjectedHashTransformBytes(entryCap+1, scratch, receiver, args, nil, NewNil()); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("checkProjectedHashTransformBytes(%d, scratch) = %v, want it to exceed the cap", entryCap+1, err)
	}
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

// TestHashBuildAccumulatorChargesReplacementsAsNetSwap pins the replacement-aware
// accounting that the merge conflict block relies on. Writing the same key many
// times (as a merge with many colliding one-key hashes does) overwrites a single
// slot, so the accumulator must charge each rewrite as the net change between the
// new value and the old, not as a fresh entry. A monotonic full-entry charge per
// write would over-count the dropped values and falsely trip a quota that
// comfortably holds the receiver and the single final entry.
func TestHashBuildAccumulatorChargesReplacementsAsNetSwap(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	exec.root = newEnv(nil)

	const payloadBytes = 4 * 1024
	const rewrites = 1_000
	base := exec.estimateMemoryUsageBase(newMemoryEstimator())
	// Headroom for the empty output map plus a handful of full entries -- far less
	// than the rewrites count, so a monotonic per-write charge would trip well
	// before the loop ends while the net-swap charge stays at a single slot.
	exec.memoryQuota = base + estimatedValueBytes + estimatedMapBaseBytes + 4*(payloadBytes+128)

	acc := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())

	// Seed the slot once, then overwrite it repeatedly with fresh, equal-sized heap
	// values, mirroring a merge conflict block that returns a new value per colliding
	// argument.
	if err := acc.add("k", NewString(strings.Repeat("x", payloadBytes))); err != nil {
		t.Fatalf("seeding the slot tripped the quota: %v", err)
	}
	afterSeed := acc.built
	for i := range rewrites {
		fresh := NewString(strings.Repeat("y", payloadBytes))
		if err := acc.add("k", fresh); err != nil {
			t.Fatalf("rewrite %d falsely tripped the quota: %v", i, err)
		}
	}

	// Net-swap accounting keeps built at one entry's worth: the seed entry plus a
	// single value swap, never growing with the rewrite count.
	if acc.built > afterSeed+payloadBytes {
		t.Fatalf("built grew to %d after %d same-key rewrites, want it to stay near one entry (%d)",
			acc.built, rewrites, afterSeed)
	}

	// A genuinely oversized replacement still trips: swapping in a value far larger
	// than the headroom must be rejected, proving the net-swap path still enforces
	// the quota on growth rather than ignoring it.
	huge := NewString(strings.Repeat("z", payloadBytes*1_000))
	err := acc.add("k", huge)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashBuildAccumulatorChargesTrackingMap pins that the accumulator's own
// keyValueBytes bookkeeping map is charged against the quota. That map keeps one
// entry per distinct output key, so a block-driven transform that retains many
// distinct keys allocates an O(n) tracking map alongside the output. Here the
// per-key value payload is a tiny inlined int, so the output entries alone stay
// comfortably under the quota -- only when the tracking map's per-entry overhead
// is also charged does the build cross the limit. The pre-fix accumulator left the
// tracking map unaccounted and would admit every insert.
func TestHashBuildAccumulatorChargesTrackingMap(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	exec.root = newEnv(nil)

	base := exec.estimateMemoryUsageBase(newMemoryEstimator())

	// Precompute the exact output-map cost of every key: the bucket, the key header,
	// the key payload, and the inlined int value. This is precisely what the pre-fix
	// accumulator charged into built; the tracking map's footprint is deliberately
	// excluded from the budget.
	const entries = 64
	keys := make([]string, entries)
	outputCost := 0
	for i := range keys {
		keys[i] = "k" + strconv.Itoa(i)
		outputCost += estimatedMapEntryBytes + estimatedStringHeaderBytes + len(keys[i]) + estimatedValueBytes
	}

	// Quota that exactly admits the empty output map plus every output entry but
	// nothing more. A build that ignores the tracking map fits all entries; one that
	// charges the tracking map's per-entry overhead must trip before the last insert.
	exec.memoryQuota = base + estimatedValueBytes + estimatedMapBaseBytes + outputCost

	acc := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())

	var tripped int
	for i, key := range keys {
		// Distinct keys whose value is an inlined int, so no heap payload is charged
		// and the only growth beyond the output entry is the tracking map's overhead.
		if err := acc.add(key, NewInt(int64(i))); err != nil {
			requireErrorIs(t, err, errMemoryQuotaExceeded)
			tripped = i
			break
		}
	}
	if tripped == 0 {
		t.Fatalf("accumulator admitted all %d entries; the tracking map's footprint was not charged", entries)
	}
}

// mergeManyCollidingArgsSource generates a script whose run() merges the receiver
// with collisions one-key hashes that all share key :x, in a single merge call, so
// every argument folds through one accumulator and overwrites the same slot. The
// conflict block returns a fresh ljust string of payloadBytes per collision, so
// each result is a distinct heap value the estimator cannot dedup to zero.
func mergeManyCollidingArgsSource(collisions, payloadBytes int) string {
	var b strings.Builder
	b.WriteString("def run(a)\n  a.merge(")
	for i := range collisions {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{ x: %d }", i+1)
	}
	fmt.Fprintf(&b, ") { |k, old, new| \"\".ljust(%d, \"z\") }\nend", payloadBytes)
	return b.String()
}

// TestHashMergeManyCollidingOneKeyHashesWithBlockSucceeds drives the replacement
// fix end to end: a single merge call folds many one-key hashes that all collide
// on the same key through a conflict block. The block returns a fresh value per
// collision, but each overwrites the same slot, so the final result holds one
// entry. A quota that comfortably fits the receiver and that single result must
// admit the merge; the pre-fix accumulator charged a full entry per collision and
// would falsely reject it.
func TestHashMergeManyCollidingOneKeyHashesWithBlockSucceeds(t *testing.T) {
	t.Parallel()

	const collisions = 400
	const payloadBytes = 4 * 1024
	receiver := NewHash(map[string]Value{"x": NewInt(0)})

	// The legitimate live footprint is the receiver, the colliding argument hashes
	// (each a one-key map of a small int), and the single final entry holding one
	// fresh payload. Estimate the arguments the script holds live and budget a quota
	// that comfortably exceeds that real footprint.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	liveArgs := make([]Value, collisions)
	for i := range liveArgs {
		liveArgs[i] = NewHash(map[string]Value{"x": NewInt(int64(i + 1))})
	}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(receiver, liveArgs, nil, NewNil())
	entryBytes := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	// Generous headroom over the live footprint: the empty output map, one full
	// payload entry, and slack for the script's AST and environment.
	quota := liveWithRoots + estimatedValueBytes + estimatedMapBaseBytes + payloadBytes + 64*entryBytes + 64*1024

	// Sanity: the pre-fix monotonic charge (a full payload entry per collision) far
	// exceeds this quota, confirming the test exercises the replacement fix rather
	// than passing trivially.
	monotonic := (collisions + 1) * (entryBytes + payloadBytes)
	if monotonic <= quota {
		t.Fatalf("test setup expects the monotonic projection (%d) to exceed the quota (%d)", monotonic, quota)
	}

	source := mergeManyCollidingArgsSource(collisions, payloadBytes)
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: quota}, source)
	got, err := script.Call(context.Background(), "run", []Value{receiver}, CallOptions{})
	if err != nil {
		t.Fatalf("merge of %d colliding one-key hashes under a fitting quota = %v, want success", collisions, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != 1 {
		t.Fatalf("merge produced %v with %d entries, want a hash with 1 entry", got.Kind(), len(got.Hash()))
	}
}

// TestHashMergeManyCollidingOneKeyHashesOversizedBlockTrips is the safety twin of
// the success case: when the conflict block returns a value far larger than the
// quota's headroom, the net-swap accounting must still reject the merge. This
// guards against the replacement path silently dropping the quota on growth.
func TestHashMergeManyCollidingOneKeyHashesOversizedBlockTrips(t *testing.T) {
	t.Parallel()

	const collisions = 50
	const payloadBytes = 1 << 20
	receiver := NewHash(map[string]Value{"x": NewInt(0)})

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(receiver, nil, nil, NewNil())
	quota := liveWithRoots + 8*1024

	source := mergeManyCollidingArgsSource(collisions, payloadBytes)
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: quota}, source)
	requireCallRuntimeErrorType(t, script, "run", []Value{receiver}, CallOptions{}, runtimeErrorTypeLimit)
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

// emptyHashBlock returns a block value whose body has no statements, mirroring an
// `h.select { }` call. callBlock charges a step only per statement it runs, so an
// empty body charges none through runner.call; the transforms must charge their
// own per-iteration step or a large receiver would walk uncharged.
func emptyHashBlock() Value {
	return NewBlock(nil, nil, newEnv(nil))
}

// TestHashEmptyBlockTransformHonorsStepQuota pins the P2 finding on PR #776: a
// block-driven hash transform invoked with an empty block must still charge a
// step per entry, so a large receiver trips the step quota even though the block
// body runs no statements. runner.call charges no step for an empty body, so each
// transform charges exec.step() itself before invoking the block. Without that an
// empty-block transform would scan the whole hash unbounded by the step quota and
// blind to cancellation.
func TestHashEmptyBlockTransformHonorsStepQuota(t *testing.T) {
	t.Parallel()

	const count = 5_000
	const stepQuota = 100

	// Block-driven hash members whose result accepts an empty block's nil return:
	// the three each* walkers (results are discarded), select and reject (nil is
	// falsy), and transform_values (any value is a valid result). transform_keys is
	// excluded because an empty block returns nil, which fails its symbol/string key
	// validation on the first entry before the quota can trip; its per-iteration step
	// charge is covered instead by the cancellation test below, where step() fails on
	// entry one before the block runs.
	for _, name := range []string{
		"each", "each_key", "each_value",
		"select", "reject", "transform_values",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			receiver := largeHashReceiver(count)
			exec := &Execution{ctx: context.Background(), quota: stepQuota, memoryQuota: 64 << 20}
			_, err := callHashMember(t, exec, receiver, name, nil, emptyHashBlock())
			requireErrorIs(t, err, errStepQuotaExceeded)
			if exec.steps > stepQuota+1 {
				t.Fatalf("%s with an empty block took %d steps, want it to stop near the quota %d", name, exec.steps, stepQuota)
			}
		})
	}
}

// TestHashEmptyBlockTransformHonorsCancellation pins the cancellation half of the
// same finding: step polls the context on its first call, so an empty-block
// transform over even a tiny receiver aborts on a canceled context rather than
// completing the walk uncharged.
func TestHashEmptyBlockTransformHonorsCancellation(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"each", "each_key", "each_value",
		"select", "reject", "transform_keys", "transform_values",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			receiver := largeHashReceiver(8)
			exec := &Execution{ctx: ctx, quota: 1 << 30, memoryQuota: 0}
			_, err := callHashMember(t, exec, receiver, name, nil, emptyHashBlock())
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s with an empty block under a canceled context = %v, want context.Canceled", name, err)
			}
		})
	}
}

// TestHashSortedKeyBufferTripsMemoryQuota pins the P2 finding on PR #776: a sorted
// hash transform materializes a []string scratch buffer of every key (one header
// per entry) outside the output-map projection. each builds no output map, so the
// scratch list is its only allocation; a quota sized to fit the live receiver and
// the empty walk but not that key buffer must reject the call up front. Before the
// fix the buffer was charged by nothing until a later check observed it, so a large
// receiver could allocate it past the sandbox limit.
func TestHashSortedKeyBufferTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	const count = 50_000
	receiver := largeHashReceiver(count)

	// The live baseline a walk holds: the call roots (receiver plus the block)
	// and the empty output-map overhead projectedHashBaseBytes folds in. each
	// builds no map, so its only extra allocation is the sorted key scratch buffer.
	block := emptyHashBlock()
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	base := probe.projectedHashBaseBytes(receiver, nil, nil, block)
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	// A quota above the live baseline (so the walk's roots fit) but below the
	// baseline plus the scratch buffer (so the buffer must be rejected). Sit the
	// quota midway so neither bound is grazed.
	quota := base + scratch/2
	if quota <= base || quota >= base+scratch {
		t.Fatalf("test setup expects base (%d) < quota (%d) < base+scratch (%d)", base, quota, base+scratch)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "each", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)

	// Sanity: a quota that also fits the scratch buffer admits the walk, proving
	// the rejection above comes from the buffer accounting and not an over-tight
	// baseline. A non-empty block would charge steps; the empty block keeps the
	// walk's only cost the scratch buffer the quota now covers.
	roomy := base + scratch + 4*1024
	exec = &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: roomy}
	if _, err := callHashMember(t, exec, receiver, "each", nil, block); err != nil {
		t.Fatalf("each under a quota that fits the key buffer = %v, want success", err)
	}
}

// TestHashSelectSortedKeyBufferTripsMemoryQuota pins the same scratch-buffer
// accounting for a map-producing transform. select projects the full output map
// plus the sorted key buffer; a quota sized to fit the live receiver and the
// output map but not the additional key buffer must reject the call before
// sorting. The block is empty so its step charge cannot mask the memory failure.
func TestHashSelectSortedKeyBufferTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	const count = 50_000
	receiver := largeHashReceiver(count)
	block := emptyHashBlock()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	base := probe.projectedHashBaseBytes(receiver, nil, nil, block)
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	outputStructure := count * perEntry
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	// Above the live baseline plus the full output map (so the old projection that
	// ignored the scratch buffer would pass) but below that plus the key buffer.
	withOutput := base + outputStructure
	quota := withOutput + scratch/2
	if quota <= withOutput || quota >= withOutput+scratch {
		t.Fatalf("test setup expects withOutput (%d) < quota (%d) < withOutput+scratch (%d)", withOutput, quota, withOutput+scratch)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "select", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}
