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
		{name: "replace", args: []Value{largeHashReceiver(count)}},
		{name: "slice", args: hashSymbolKeys(count)},
		{name: "store", args: []Value{NewSymbol("extra"), NewInt(1)}},
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
		{name: "replace", args: []Value{largeHashReceiver(8)}},
		{name: "slice", args: hashSymbolKeys(8)},
		{name: "store", args: []Value{NewSymbol("extra"), NewInt(1)}},
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

// TestHashReplaceFitsOutputQuotaWithoutScratch pins the final finding on PR #776:
// Hash#replace copies the replacement into an order-independent output map, so it
// iterates the replacement directly rather than materializing a sorted key list.
// Its blockless preflight (checkProjectedHashBytes) therefore charges only the
// output map and no scratch buffer, so a quota sized to exactly the live call
// roots plus that output map must admit the call over a large replacement.
//
// This is the no-scratch counterpart to TestHashSelectSortedKeyBufferTripsMemory-
// Quota, which pins that map-producing transforms which DO sort charge their
// scratch. If replace ever regrows a sorted walk and (correctly) charges the
// sortedKeyBufferBytes(count) buffer through the transform preflight, this tight
// output-only quota would start rejecting and this test would fail, forcing the
// reviewer to confront the reintroduced cost. It also exercises the in-place copy
// over enough entries to spill past the inline key buffer, guarding the order-
// independent correctness of the result against a large replacement.
func TestHashReplaceFitsOutputQuotaWithoutScratch(t *testing.T) {
	t.Parallel()

	// Enough keys that a reintroduced sorted-key list would heap a real buffer
	// rather than reusing the inline stack array, so its omission is a meaningful
	// share of the budget the no-scratch quota deliberately withholds.
	const count = 50_000
	receiver := largeHashReceiver(count)
	replacement := largeHashReceiver(count)

	// Size the quota to exactly what replace allocates: the live call roots
	// (receiver plus the replacement argument) and the output map of
	// len(replacement) entries. No scratch term -- replace iterates in place.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	base := probe.projectedHashBaseBytes(receiver, []Value{replacement}, nil, NewNil())
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	outputStructure := count * perEntry
	quota := base + outputStructure

	// Sanity: a sorted-key scratch buffer for this many entries is non-trivial, so
	// the no-scratch quota genuinely withholds it. A walk that charged that buffer
	// through the preflight could not fit this budget, proving the fit pins the in-
	// place copy rather than an incidentally roomy quota.
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "replace", []Value{replacement}, NewNil())
	if err != nil {
		t.Fatalf("replace under an output-sized quota = %v, want success", err)
	}
	if got.Kind() != KindHash {
		t.Fatalf("replace returned %v, want hash", got.Kind())
	}
	if len(got.Hash()) != count {
		t.Fatalf("replace produced %d entries, want %d", len(got.Hash()), count)
	}
	for key, want := range replacement.Hash() {
		if got.Hash()[key].String() != want.String() {
			t.Fatalf("replace entry %q = %v, want %v", key, got.Hash()[key], want)
		}
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
	scratch := mergeSortScratchBytes(args)
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

// TestHashExceptChargesExclusionSet pins the P1 except finding on PR #776: the
// exclusion set built from candidate keys present in the receiver is live
// alongside the freshly copied output map, so its footprint must be projected too.
// h.except(*h.keys) excludes every key, so the exclusion set holds one entry per
// receiver entry while the worst-case-sized output map is allocated. The pre-fix
// projection charged only the output copy, so a quota sized for receiver + output
// admitted the call and then allocated the unaccounted set, exceeding the limit
// during the call (gone before the post-call check could observe the peak).
func TestHashExceptChargesExclusionSet(t *testing.T) {
	t.Parallel()

	const count = 20_000
	receiver := largeHashReceiver(count)
	// h.except(*h.keys): the candidate keys are exactly the receiver's keys, so the
	// exclusion set reaches one entry per receiver entry.
	args := make([]Value, 0, count)
	for key := range receiver.Hash() {
		args = append(args, NewSymbol(key))
	}

	// Measure the live call-root footprint and the worst-case output map (except
	// projects a full copy because absent candidates would leave every entry in
	// place), then size the quota to admit exactly that pre-fix budget. The
	// exclusion set's footprint is the only charge that pushes the build over, so a
	// quota that fits roots + output but not the set proves the set is now charged.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(receiver, args, nil, NewNil())
	outputStructure := estimatedValueBytes + estimatedMapBaseBytes +
		count*(estimatedMapEntryBytes+estimatedStringHeaderBytes+estimatedValueBytes)
	preFixBudget := liveWithRoots + outputStructure

	exclusion := exclusionSetBytes(count)
	if exclusion <= 0 {
		t.Fatalf("expected a positive exclusion-set footprint, got %d", exclusion)
	}

	// Above the pre-fix budget (the old projection of roots + output would pass) but
	// below roots + output + exclusion set (the new projection must reject).
	quota := preFixBudget + exclusion/2

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "except", args, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)

	// A quota generously above roots + output + exclusion set still admits the call
	// and returns the empty result, proving the new charge does not over-tighten a
	// valid except.
	roomy := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: preFixBudget + exclusion + 64*1024}
	out, err := callHashMember(t, roomy, receiver, "except", args, NewNil())
	if err != nil {
		t.Fatalf("except within an ample quota failed: %v", err)
	}
	if got := len(out.Hash()); got != 0 {
		t.Fatalf("except(*keys) kept %d entries, want 0", got)
	}
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
	// quota above the live baseline admits roughly three before tripping. The
	// backing reservation for the preallocated slots is folded into the quota so the
	// trip comes from the accumulated payloads, not the reserved structure.
	const payloadBytes = 4 * 1024
	const capacity = 100
	base := exec.estimateMemoryUsageBase(newMemoryEstimator())
	exec.memoryQuota = base + estimatedValueBytes + estimatedMapBaseBytes +
		capacity*estimatedMapEntryStructuralBytes + 3*payloadBytes

	acc := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	// Mirror a transform that preallocates make(map, capacity): the full backing is
	// reserved up front, so add charges only each block result's payload.
	if err := acc.reserveBacking(capacity); err != nil {
		t.Fatalf("reserving the backing tripped the quota before any entry: %v", err)
	}

	var tripped int
	for i := range capacity {
		// Distinct value backings so nothing dedups to zero: each add charges a fresh
		// payload, mirroring a block that returns new heap values per entry.
		fresh := NewString(strings.Repeat("x", payloadBytes))
		if err := acc.add(fresh); err != nil {
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

// TestHashBuildAccumulatorReservesScratch pins the P1 finding on PR #776: the
// sorted-key scratch buffer is live for the whole block-driven build, coexisting
// with the output map at peak, so the accumulator must reserve its bytes for the
// build's lifetime. reserveScratch folds the scratch into the baseline, shrinking
// the headroom by exactly the scratch size; an entry that fits without the
// reservation but not with it must be rejected once the scratch is reserved.
// Before the fix the accumulator's running budget omitted the scratch entirely, so
// the combined output+scratch peak could exceed the quota by the scratch size.
func TestHashBuildAccumulatorReservesScratch(t *testing.T) {
	t.Parallel()

	// A large but finite memory quota: the accumulator only runs its accounting when
	// a positive quota is set, so the probe below must measure under a real (ample)
	// quota rather than the unbounded (memoryQuota == 0) short circuit.
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	exec.root = newEnv(nil)

	const payloadBytes = 4 * 1024

	// Measure the accumulator's exact built footprint after one entry and after two,
	// under an ample quota, so the test sizes the budget from the real per-entry cost
	// rather than a hand-derived estimate that could drift if the accounting changes.
	// The fresh string payloads dedup against nothing, so each entry charges its full
	// cost.
	probe := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	if err := probe.add(NewString(strings.Repeat("x", payloadBytes))); err != nil {
		t.Fatalf("probe first entry error = %v", err)
	}
	builtOneEntry := probe.built
	if err := probe.add(NewString(strings.Repeat("y", payloadBytes))); err != nil {
		t.Fatalf("probe second entry error = %v", err)
	}
	secondEntryCost := probe.built - builtOneEntry

	// A scratch smaller than one entry's cost: with it reserved the first entry still
	// fits, so the rejection lands cleanly on the second add and is attributable to
	// the scratch reservation rather than to the first entry overflowing on its own.
	scratchBytes := secondEntryCost / 2
	if scratchBytes <= 0 {
		t.Fatalf("test setup expects a positive scratch derived from the entry cost (%d)", secondEntryCost)
	}

	// A quota that admits the empty output map plus exactly one full entry and the
	// reserved scratch, but would admit a second entry if the scratch were not
	// reserved. The second entry costs more than the scratch, so without the
	// reservation the leftover headroom (a full entry's worth) covers it; with the
	// reservation only the scratch's worth of headroom remains, which is too small.
	emptyMap := estimatedValueBytes + estimatedMapBaseBytes
	base := probe.base - emptyMap // probe.base folds in the empty-map overhead
	exec.memoryQuota = base + emptyMap + builtOneEntry + secondEntryCost

	// Without reserving the scratch, both entries fit the budget, proving the
	// reservation -- not the entry payloads alone -- is what rejects the second.
	unreserved := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	if err := unreserved.add(NewString(strings.Repeat("x", payloadBytes))); err != nil {
		t.Fatalf("first entry tripped the quota without a scratch reservation: %v", err)
	}
	if err := unreserved.add(NewString(strings.Repeat("y", payloadBytes))); err != nil {
		t.Fatalf("test setup expects two entries to fit without the scratch reservation: %v", err)
	}

	// Reserving the scratch consumes the headroom the second entry relied on, so the
	// first entry fits but the second is rejected.
	reserved := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	if err := reserved.reserveScratch(scratchBytes); err != nil {
		t.Fatalf("reserving the scratch tripped the quota before any entry: %v", err)
	}
	if err := reserved.add(NewString(strings.Repeat("x", payloadBytes))); err != nil {
		t.Fatalf("first entry tripped the quota with the scratch reserved, want it to fit: %v", err)
	}
	err := reserved.add(NewString(strings.Repeat("y", payloadBytes)))
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashBuildAccumulatorReservesBacking pins the latest P1 finding on PR #776:
// the output map is allocated with make(map, capacity), so its full capacity
// backing is live before the first block result is charged. The accumulator must
// reserve that whole backing in the baseline up front; otherwise a large EARLY
// block result is checked against only the slots filled so far, letting the
// backing plus that result exceed the quota before later entries (or the post-call
// check) caught it.
//
// The test drives the accumulator directly with one large early result and a quota
// sized to admit base + empty map + one entry's payload + a single backing slot,
// but NOT the full capacity backing. With the backing reserved the early result is
// rejected; without it the identical result is wrongly admitted -- exactly the
// transient under-count the reservation closes.
func TestHashBuildAccumulatorReservesBacking(t *testing.T) {
	t.Parallel()

	// A capacity far larger than one entry, so the reserved backing dominates the
	// budget: the gap between "one slot" and "capacity slots" is what the fix charges.
	const capacity = 10_000
	const payloadBytes = 4 * 1024

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	exec.root = newEnv(nil)

	// Measure the empty-map baseline and one result's payload under an ample quota so
	// the budget is sized from the real accounting rather than a hand-derived estimate.
	probe := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	emptyMapBase := probe.base
	if err := probe.add(NewString(strings.Repeat("x", payloadBytes))); err != nil {
		t.Fatalf("probe result tripped under an unbounded quota: %v", err)
	}
	resultPayload := probe.built

	// A quota that admits the empty-map baseline, the one result's payload, and a
	// single backing slot -- but not the full capacity backing. The early result fits
	// when only one slot is reserved (the buggy view) yet overflows once the whole
	// capacity backing is charged (the fixed view).
	exec.memoryQuota = emptyMapBase + resultPayload + estimatedMapEntryStructuralBytes

	// Without reserving the backing, the single early result is wrongly admitted: the
	// running budget sees only the slots filled so far, not the live capacity backing.
	unreserved := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	if err := unreserved.add(NewString(strings.Repeat("x", payloadBytes))); err != nil {
		t.Fatalf("without the backing reservation the early result should be wrongly admitted, got %v", err)
	}

	// Reserving the full capacity backing first -- exactly what the transform's
	// make(map, capacity) allocates -- rejects the same early result, since the backing
	// plus the result already exceeds the quota.
	reserved := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	if err := reserved.reserveBacking(capacity); err != nil {
		requireErrorIs(t, err, errMemoryQuotaExceeded)
		return
	}
	err := reserved.add(NewString(strings.Repeat("x", payloadBytes)))
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashBuildAccumulatorBackingReservationMatchesProjection pins that the
// accumulator's reserved backing equals exactly what the up-front projection
// charges for the same capacity, so the running budget and the preflight reserve
// the same bytes. A drift between the two views would either reject builds the
// projection admitted or admit builds it rejected. The build's final base after
// reserveBacking(capacity) must equal the call-root usage plus the empty map plus
// capacity structural slots -- the same outputEntries*perEntry the projection adds.
func TestHashBuildAccumulatorBackingReservationMatchesProjection(t *testing.T) {
	t.Parallel()

	const capacity = 4_096
	receiver := largeHashReceiver(capacity)

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	exec.root = newEnv(nil)

	acc := newHashBuildAccumulator(exec, receiver, nil, nil, NewNil())
	if err := acc.reserveBacking(capacity); err != nil {
		t.Fatalf("reserving the backing tripped under an unbounded quota: %v", err)
	}

	// The projection's baseline (call roots + empty map) plus capacity structural slots
	// is exactly what the accumulator's base must hold after reserveBacking, proving the
	// two budgets reserve the same backing.
	wantBase := exec.projectedHashBaseBytes(receiver, nil, nil, NewNil()) +
		capacity*estimatedMapEntryStructuralBytes
	if acc.base != wantBase {
		t.Fatalf("accumulator base after reserveBacking = %d, want %d (the projection's backing)", acc.base, wantBase)
	}
}

// TestHashTransformValuesScratchPeakTripsMemoryQuota drives the P1 scratch
// reservation through transform_values: the sorted-key scratch buffer is live for
// the whole build, coexisting with the output map at peak, so it must be charged
// against the running budget rather than only the up-front projection. The quota
// is sized to exactly the accumulator's peak with the scratch reserved minus one
// byte, so the call is rejected precisely because of the scratch; granting the
// scratch's bytes back admits the identical build. Before the fix the accumulator
// ignored the scratch, so the combined output+scratch peak slipped past the quota
// until the post-call check.
func TestHashTransformValuesScratchPeakTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// Enough keys that the sorted-key list heaps a real buffer rather than reusing
	// the inline stack array, so its reservation is a meaningful share of the budget.
	const count = 20_000
	receiver := largeHashReceiver(count)
	// An empty block makes transform_values map every key to nil: nil is a valid
	// result value with no heap payload, so the build's only allocations are the
	// output map's structural slots, the accumulator's tracking map, and the sorted
	// key scratch buffer. That keeps the peak exactly computable end to end.
	block := emptyHashBlock()

	// Compute the accumulator's exact peak the build reaches, then add the scratch
	// the live key list holds alongside it. The probe runs under an ample (but
	// finite) quota so its accounting executes -- a zero quota would short-circuit
	// every add -- while staying large enough never to trip, letting us read the
	// final built footprint.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	acc := newHashBuildAccumulator(probe, receiver, nil, nil, block)
	// Mirror the real transform: it preallocates make(map, count) and reserves that
	// backing before charging block results, so the probe must reserve it too or the
	// scratch-blind peak would under-count the live backing.
	if err := acc.reserveBacking(count); err != nil {
		t.Fatalf("reserving the backing tripped under an unbounded quota: %v", err)
	}
	for range sortedHashKeysInto(receiver.Hash(), nil) {
		if err := acc.add(NewNil()); err != nil {
			t.Fatalf("probe build tripped under an unbounded quota: %v", err)
		}
	}
	peakWithoutScratch := acc.base + acc.built
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	// A quota one byte short of the peak plus the reserved scratch: the build fits
	// without the scratch (peakWithoutScratch <= quota) but not with it.
	quota := peakWithoutScratch + scratch - 1
	if peakWithoutScratch > quota {
		t.Fatalf("test setup expects the scratch-blind peak (%d) to fit the quota (%d)", peakWithoutScratch, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "transform_values", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)

	// Sanity: granting exactly the scratch budget back admits the identical build,
	// proving the rejection above is the scratch reservation and not an over-tight
	// quota. The empty block charges steps per entry; the ample step quota lets the
	// memory accounting be the only constraint.
	roomy := quota + scratch + 1
	exec = &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: roomy}
	got, err := callHashMember(t, exec, receiver, "transform_values", nil, block)
	if err != nil {
		t.Fatalf("transform_values under a quota that fits the scratch = %v, want success", err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != count {
		t.Fatalf("transform_values produced %v with %d entries, want a hash with %d", got.Kind(), len(got.Hash()), count)
	}
}

// TestHashEachFitsRealFootprint pins the P2 finding on PR #776: pure iterators
// (each, each_key, each_value) build no output map -- they return the receiver --
// so charging them an empty output map's overhead is a pure over-charge. A quota
// sized to the iterator's real footprint (the live receiver and the sorted-key
// scratch buffer, with no derived map) must admit the walk. Before the fix the
// projection folded in an empty output map the iterator never allocates, so a quota
// that exactly fit the real footprint was falsely rejected.
func TestHashEachFitsRealFootprint(t *testing.T) {
	t.Parallel()

	const count = 20_000
	receiver := largeHashReceiver(count)
	block := emptyHashBlock()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	for _, name := range []string{"each", "each_key", "each_value"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A quota that exactly covers the call roots and the scratch buffer: the
			// iterator's true peak. The empty block charges no steps and builds no map,
			// so this is everything the walk needs.
			quota := roots + scratch
			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
			got, err := callHashMember(t, exec, receiver, name, nil, block)
			if err != nil {
				t.Fatalf("%s under a footprint-sized quota = %v, want success", name, err)
			}
			if got.Kind() != KindHash || len(got.Hash()) != count {
				t.Fatalf("%s returned %v with %d entries, want the receiver with %d", name, got.Kind(), len(got.Hash()), count)
			}

			// The discarded projection added an empty output map (value slot plus map
			// base) the iterator never allocates. A quota one byte below the real
			// footprint must still reject, proving the success above is not slack from
			// a quota that happens to also cover the phantom map.
			tight := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota - 1}
			if _, err := callHashMember(t, tight, receiver, name, nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
				t.Fatalf("%s one byte below its footprint = %v, want errMemoryQuotaExceeded", name, err)
			}
		})
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

// TestHashMergeManyCollidingOneKeyHashesWithBlockConservativeFootprint pins the
// conservative block-result accounting end to end: a single merge call folds many
// one-key hashes that all collide on the same key through a conflict block. The
// block returns a fresh value per collision, and although each overwrites the same
// slot so the final result holds one entry, the accumulator charges every block
// result its full footprint as it is produced. That conservative counting is what
// keeps the bound sound under in-place mutation, at the cost of over-counting a
// collapsed slot: the running total tracks the sum of every block result, not the
// single final entry.
//
// The test exercises both halves of that contract. A quota sized to the
// conservative footprint (every collision's payload, plus headroom) admits the
// merge; a quota sized only to the single final entry -- which the pre-fix
// replacement-aware accumulator would have accepted -- is now correctly rejected,
// because the transient block results accumulate past it.
func TestHashMergeManyCollidingOneKeyHashesWithBlockConservativeFootprint(t *testing.T) {
	t.Parallel()

	const collisions = 400
	const payloadBytes = 4 * 1024
	receiver := NewHash(map[string]Value{"x": NewInt(0)})

	// The live footprint is the receiver, the colliding argument hashes (each a
	// one-key map of a small int), and the conservative output charge: one full entry
	// per collision (the block result is charged once per write).
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	liveArgs := make([]Value, collisions)
	for i := range liveArgs {
		liveArgs[i] = NewHash(map[string]Value{"x": NewInt(int64(i + 1))})
	}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(receiver, liveArgs, nil, NewNil())
	entryBytes := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	// The conservative footprint: a full payload entry per collision, since each
	// block result is charged once when it is written.
	conservative := collisions * (entryBytes + payloadBytes)

	// A quota above the conservative footprint admits the merge.
	roomy := liveWithRoots + estimatedValueBytes + estimatedMapBaseBytes + conservative + 64*1024
	source := mergeManyCollidingArgsSource(collisions, payloadBytes)
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: roomy}, source)
	got, err := script.Call(context.Background(), "run", []Value{receiver}, CallOptions{})
	if err != nil {
		t.Fatalf("merge of %d colliding one-key hashes under a conservative-sized quota = %v, want success", collisions, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != 1 {
		t.Fatalf("merge produced %v with %d entries, want a hash with 1 entry", got.Kind(), len(got.Hash()))
	}

	// A quota sized only to the single final entry -- enough for the replacement-aware
	// accounting this PR removed, but far below the conservative footprint -- is now
	// rejected, because the transient block results accumulate past it.
	tight := liveWithRoots + estimatedValueBytes + estimatedMapBaseBytes + payloadBytes + 64*entryBytes + 64*1024
	if tight >= conservative {
		t.Fatalf("test setup expects the single-entry quota (%d) below the conservative footprint (%d)", tight, conservative)
	}
	tightScript := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: tight}, source)
	requireCallRuntimeErrorType(t, tightScript, "run", []Value{receiver}, CallOptions{}, runtimeErrorTypeLimit)
}

// TestHashMergeManyCollidingOneKeyHashesOversizedBlockTrips is the safety twin of
// the conservative-footprint case: when the conflict block returns a value far
// larger than the quota's headroom, the accumulator must reject the merge. This
// guards against the block-result accounting silently dropping the quota on growth.
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

// TestHashBuildAccumulatorChargesInPlaceMutatedBaseContainer pins the P1 finding on
// PR #776 (memory.go:676): a block that mutates a receiver-owned container in place
// -- appending a large fresh payload into an array that still has spare capacity --
// and returns it. The array's backing was already in the call-root baseline before
// the block ran, but the newly stored element payload was not. The conservative
// accumulator's results-only estimator is deliberately NOT seeded with the
// baseline, so it charges the returned value at its full current size and observes
// the fresh payload. The removed base-seeded dedup machinery saw the backing in the
// baseline's seen-set and deduplicated the whole array (fresh payload included) to
// ~0, under-counting the live result mid-build -- a sandbox escape, since the
// accumulator runs precisely where the post-call safety net cannot (the mutated
// array's transient mid-build footprint).
//
// The accumulator is driven directly with controlled Values so the under-count is
// isolated: a receiver array with spare capacity is passed as a call root (folded
// into the baseline), then the block "returns" that same array after a large string
// is stored into a previously-empty slot. With conservative counting the fresh
// payload pushes the running total past a quota sized to the array's original
// footprint, so the build is rejected.
func TestHashBuildAccumulatorChargesInPlaceMutatedBaseContainer(t *testing.T) {
	t.Parallel()

	const payload = 256 * 1024

	// A receiver array with one filled slot and spare capacity: the block appends
	// into the spare slot (within capacity, so the backing pointer is unchanged) and
	// returns the same array. The backing is receiver-owned, so it is in the call-root
	// baseline before the block runs.
	backing := make([]Value, 1, 2)
	backing[0] = NewInt(0)
	receiverArray := NewArray(backing)
	receiver := NewHash(map[string]Value{"a": receiverArray})

	// Learn the accumulator's baseline (which counts the receiver array once) under no
	// memory pressure, so the quota can sit between the array's original footprint and
	// its footprint plus the fresh payload.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1}
	probeAcc := newHashBuildAccumulator(probe, receiver, nil, nil, NewNil())
	base := probeAcc.base

	// A quota above the baseline (so the array fits as the receiver holds it) but well
	// below the baseline plus the fresh payload (so charging the mutated array's full
	// current size must trip).
	quota := base + payload/2

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	acc := newHashBuildAccumulator(exec, receiver, nil, nil, NewNil())

	// Mutate the receiver-owned backing in place: append a large string into the spare
	// capacity. The append keeps the same backing pointer (cap headroom), mirroring an
	// in-place block mutation like `v << big` or `v[1] = big` on a receiver value.
	mutated := append(backing, NewString(strings.Repeat("x", payload)))
	if sliceBackingIdentity(mutated) != sliceBackingIdentity(backing) {
		t.Fatalf("test setup expects the append to stay within capacity (same backing)")
	}
	mutatedArray := NewArray(mutated)

	// Conservative counting charges the returned value's full current payload, so the
	// fresh payload is observed and the quota is tripped. The removed base-seeded dedup
	// would have charged ~0 here (the backing was in the baseline) and admitted it.
	err := acc.add(mutatedArray)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashMergeBaseSeedingWouldUndercountConflictResult pins the exact P1 finding
// on PR #776: merge's base-copy loop must NOT charge receiver entries through the
// results-only estimator before the conflict block runs. The buggy loop did
// (acc.add per base entry), which recorded the receiver's array backings
// in the estimator's seen-set. A conflict block that mutated one of those arrays in
// place (within spare capacity, keeping the same backing) and returned it was then
// deduplicated against the seeded backing and charged ~0 for the freshly stored
// payload -- the under-count that escapes the quota.
//
// The test reproduces both seeding orders against one accumulator each. Seeding the
// base first (the bug) deduplicates the mutated array on the conflict charge, so the
// fresh payload slips past a quota the conflict result alone should trip. Not seeding
// the base (the fix) charges the conflict result at full current size and trips. The
// gap between the two is exactly the undercount the fix closes.
func TestHashMergeBaseSeedingWouldUndercountConflictResult(t *testing.T) {
	t.Parallel()

	const payload = 256 * 1024

	// A receiver array with spare capacity, holding key "x" -- the conflict key.
	backing := make([]Value, 1, 2)
	backing[0] = NewInt(0)
	receiverArray := NewArray(backing)
	receiver := NewHash(map[string]Value{"x": receiverArray})
	arg := NewHash(map[string]Value{"x": NewInt(1)})

	// Baseline counts the receiver array once via the call roots. The quota sits above
	// it (the array fits as held) but below it plus the fresh payload, so charging the
	// conflict result's full current footprint must trip while a dedup'd ~0 charge does
	// not.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1}
	base := newHashBuildAccumulator(probe, receiver, []Value{arg}, nil, NewNil()).base
	quota := base + payload/2

	// The conflict block mutates the receiver-owned backing in place (old[1] = big) and
	// returns the same array, mirroring `old[1] = big; old`.
	mutated := append(backing, NewString(strings.Repeat("x", payload)))
	if sliceBackingIdentity(mutated) != sliceBackingIdentity(backing) {
		t.Fatalf("test setup expects the append to stay within capacity (same backing)")
	}
	conflictResult := NewArray(mutated)

	// Buggy order: seed the base entry first (what merge's base-copy loop used to do),
	// then charge the conflict result. The seeded backing dedups the conflict result, so
	// the fresh payload is NOT counted and the build is wrongly admitted -- the
	// under-count this regression test exists to forbid in the production path.
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	seeded := newHashBuildAccumulator(exec, receiver, []Value{arg}, nil, NewNil())
	if err := seeded.add(receiverArray); err != nil {
		t.Fatalf("seeding the base entry tripped the quota before any conflict: %v", err)
	}
	if err := seeded.add(conflictResult); err != nil {
		t.Fatalf("base-seeded accumulator unexpectedly tripped (%v); the test must demonstrate the undercount the buggy seeding produces", err)
	}

	// Fixed order: the base entry is never charged through the accumulator (merge's
	// base-copy loop no longer calls acc.add), so the conflict result is the first thing
	// the estimator sees and is charged at full current size, observing the fresh payload
	// and tripping the same quota. The difference between the two branches is exactly the
	// sandbox escape the fix closes.
	fixed := newHashBuildAccumulator(exec, receiver, []Value{arg}, nil, NewNil())
	err := fixed.add(conflictResult)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashTransformKeysSynthesizedKeyChargesKeyNotValue audits the second half of
// the P1 finding on PR #776: transform_keys' block returns the KEY while the value
// stays a receiver value already counted in the baseline. The earlier code charged
// the entry through add(resolved, entries[key]), routing the receiver value through
// the results-only estimator and recording its backing in the seen-set -- a
// receiver/argument value reaching the estimator, the same class of bug as merge's
// base seeding. addSynthesizedKey charges only the fresh key's payload (the entry's
// structural slot is reserved up front by reserveBacking), so the receiver value's
// backing is never recorded.
//
// The test asserts both halves: the charge equals the synthesized key's payload
// only (not the value's reachable payload), and the value's backing is absent from
// the estimator's seen-set afterward, so a later block result that aliased it would
// still be charged at full size.
func TestHashTransformKeysSynthesizedKeyChargesKeyNotValue(t *testing.T) {
	t.Parallel()

	// The receiver value is a large array: routing it through the estimator would both
	// over-count it (its payload is already in the baseline) and seed its backing.
	const payload = 4096
	valueBacking := make([]Value, payload)
	for i := range valueBacking {
		valueBacking[i] = NewInt(int64(i))
	}
	receiverValue := NewArray(valueBacking)
	receiver := NewHash(map[string]Value{"orig": receiverValue})

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	acc := newHashBuildAccumulator(exec, receiver, nil, nil, NewNil())

	const synthesized = "freshkey"
	if err := acc.addSynthesizedKey(synthesized); err != nil {
		t.Fatalf("addSynthesizedKey tripped under an unbounded quota: %v", err)
	}

	// The charge is only the fresh key's payload -- the entry's structural slot (map
	// bucket, key header, value slot) is reserved up front by reserveBacking, and the
	// value's reachable payload is already in the baseline as a receiver value, so
	// neither is charged here.
	want := len(synthesized)
	if acc.built != want {
		t.Fatalf("addSynthesizedKey charged %d bytes, want %d (the synthesized key payload only)", acc.built, want)
	}

	// The receiver value's backing must be absent from the estimator's seen-set: a later
	// block result that aliased it must still be charged its full footprint. Charging the
	// receiver value through a fresh estimator must therefore measure its full payload,
	// proving addSynthesizedKey left it unseen.
	if got := acc.est.value(receiverValue); got <= estimatedValueBytes {
		t.Fatalf("receiver value charged %d bytes after addSynthesizedKey, want its full footprint (the value backing must not be seeded)", got)
	}
}

// TestHashBuildAccumulatorChargesCyclicBlockResult pins that the conservative
// accumulator handles a cyclic block result safely: a transform block can return a
// value that reaches itself through in-place index assignment (a = [0]; a[0] = a, or
// obj["self"] = obj). The results-only estimator's own seen-sets terminate the walk
// within a single value, so charging the cyclic value is bounded and finite -- it
// neither recurses forever nor over-charges the self-edge. Charging it once must fit
// an ample quota.
func TestHashBuildAccumulatorChargesCyclicBlockResult(t *testing.T) {
	t.Parallel()

	shapes := []struct {
		name  string
		build func() Value
	}{
		{name: "self_cyclic_array", build: selfCyclicArray},
		{name: "self_cyclic_hash", build: selfCyclicHash},
		{
			name: "cycle_nested_under_fresh_container",
			build: func() Value {
				return NewArray([]Value{selfCyclicArray()})
			},
		},
	}

	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()

			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
			exec.root = newEnv(nil)
			base := exec.estimateMemoryUsageBase(newMemoryEstimator())
			// Ample headroom: a single cyclic value is tiny, so the quota only needs to
			// admit one entry. The point is that the walk terminates and charges a finite
			// amount rather than looping on the self-edge.
			exec.memoryQuota = base + estimatedValueBytes + estimatedMapBaseBytes + 64*1024

			acc := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
			if err := acc.add(shape.build()); err != nil {
				t.Fatalf("charging a cyclic block result tripped an ample quota: %v", err)
			}
			if acc.built <= 0 {
				t.Fatalf("cyclic block result charged %d bytes, want a positive finite charge", acc.built)
			}
		})
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

// selfCyclicArray builds an array whose single element points back at the array
// itself: a = [0]; a[0] = a. NewArray stores the slice header directly, so
// mutating the backing after construction makes the value reach itself, mirroring
// a Vibescript script that builds a cycle with in-place index assignment.
func selfCyclicArray() Value {
	backing := make([]Value, 1)
	a := NewArray(backing)
	backing[0] = a
	return a
}

// selfCyclicHash builds a hash that holds a reference to itself under one key:
// obj = {}; obj["self"] = obj, the map analog of selfCyclicArray.
func selfCyclicHash() Value {
	backing := make(map[string]Value, 1)
	h := NewHash(backing)
	backing["self"] = h
	return h
}

// TestHashMergeZeroArgWithBlockOverLargeReceiverSucceeds pins the P2 finding on PR
// #776: a bare `h.merge { ... }` with no argument hashes short-circuits to a copy
// of the receiver and never runs the block or sorts the base, so the conflict
// block's base scratch buffer is never allocated. The projection must not charge
// that phantom scratch. A receiver whose real copy fits the quota but whose
// phantom base scratch would not must still be admitted.
func TestHashMergeZeroArgWithBlockOverLargeReceiverSucceeds(t *testing.T) {
	t.Parallel()

	const count = 50_000
	receiver := largeHashReceiver(count)

	// Size the quota to fit the live receiver plus the copied output map exactly,
	// with no headroom for a base-sized sorted key scratch buffer. The phantom
	// scratch the pre-fix projection charged would push this over the limit.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(receiver, nil, nil, NewNil())
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	outputStructure := estimatedValueBytes + estimatedMapBaseBytes + count*perEntry
	scratch := sortedKeyBufferBytes(count)
	quota := liveWithRoots + outputStructure + scratch/2

	// Sanity: the pre-fix projection added the full base scratch on top, which would
	// exceed this quota, so admitting the call proves the phantom charge is gone.
	if liveWithRoots+outputStructure+scratch <= quota {
		t.Fatalf("test setup expects the phantom-scratch projection (%d) to exceed the quota (%d)", liveWithRoots+outputStructure+scratch, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "merge", nil, emptyHashBlock())
	if err != nil {
		t.Fatalf("zero-arg merge with block over a fitting receiver = %v, want success", err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != count {
		t.Fatalf("zero-arg merge produced %v with %d entries, want a hash with %d", got.Kind(), len(got.Hash()), count)
	}
}

// TestHashMergeZeroArgHonorsStepQuotaOnBaseCopy pins the P2 finding on PR #776:
// a zero-argument `h.merge` (or `h.merge {}`) short-circuits to a copy of the
// receiver, so the only work it does is copying the base. That copy must walk the
// receiver entry by entry charging a step each, not maps.Copy the whole map at
// once, or a large receiver duplicates unbounded by the step quota. The existing
// merge subtests in TestHashBlocklessTransformHonorsStepQuota pass a non-empty
// argument hash, so their additions loop trips the quota whether or not the base
// copy steps; only a zero-argument merge isolates the base-copy path. A tight step
// quota with ample memory must trip on errStepQuotaExceeded; reverting the base
// copy to maps.Copy would skip the per-entry step and let this complete.
func TestHashMergeZeroArgHonorsStepQuotaOnBaseCopy(t *testing.T) {
	t.Parallel()

	const count = 5_000
	const stepQuota = 100

	// Both the bare `h.merge` (nil block) and `h.merge {}` (empty block) forms
	// short-circuit to the base copy with zero arguments, so both must charge a
	// step per copied entry.
	for _, tc := range []struct {
		name  string
		block Value
	}{
		{name: "no block", block: NewNil()},
		{name: "empty block", block: emptyHashBlock()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			receiver := largeHashReceiver(count)
			exec := &Execution{ctx: context.Background(), quota: stepQuota, memoryQuota: 64 << 20}
			_, err := callHashMember(t, exec, receiver, "merge", nil, tc.block)
			requireErrorIs(t, err, errStepQuotaExceeded)
			if exec.steps > stepQuota+1 {
				t.Fatalf("zero-arg merge took %d steps, want it to stop near the quota %d", exec.steps, stepQuota)
			}
		})
	}
}

// TestHashMergeZeroArgHonorsCancellationOnBaseCopy is the cancellation half of the
// same finding: step polls the context on its first call, so the base copy of a
// zero-argument merge aborts on a canceled context before copying any entries.
// Reverting the base copy to maps.Copy would skip that poll and complete the copy.
func TestHashMergeZeroArgHonorsCancellationOnBaseCopy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		block Value
	}{
		{name: "no block", block: NewNil()},
		{name: "empty block", block: emptyHashBlock()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			receiver := largeHashReceiver(8)
			exec := &Execution{ctx: ctx, quota: 1 << 30, memoryQuota: 0}
			_, err := callHashMember(t, exec, receiver, "merge", nil, tc.block)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("zero-arg merge under a canceled context = %v, want context.Canceled", err)
			}
		})
	}
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

	// The live baseline a walk holds: the call roots (receiver plus the block).
	// each builds no output map, so its baseline is the call roots alone (no
	// empty-map overhead) and its only extra allocation is the sorted key scratch
	// buffer.
	block := emptyHashBlock()
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	base := probe.hashCallRootBytes(receiver, nil, nil, block)
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

// TestForHashLoopSortedKeyBufferTripsMemoryQuota pins the scratch-buffer
// accounting for `for pair in hash`. Like hash.each, a `for` over a hash builds no
// output map but materializes a sorted []string key buffer (one header per entry)
// to walk entries deterministically. That buffer must be charged before it is
// allocated so a large iterable cannot allocate it past the sandbox limit; the
// statement-level checkMemoryWith(iterable) only re-counts the already-resident
// hash and never adds the scratch footprint. Before the fix the loop re-counted
// only the iterable and admitted the buffer.
func TestForHashLoopSortedKeyBufferTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	const count = 2_000
	receiver := largeHashReceiver(count)

	// The live baseline the loop holds: the hash bound to the run() parameter the
	// loop iterates. `for` builds no output map, so its baseline is the call roots
	// alone and its only extra allocation is the sorted key scratch buffer. The
	// loop's runtime check (checkProjectedHashWalkBytes) charges the same shape:
	// estimateMemoryUsageBase plus the iterable, deduplicated against the base.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	base := probe.estimateMemoryUsageForCallRoots(receiver, nil, nil, NewNil())
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	source := `def run(values)
  for pair in values
  end
end`

	// A quota above the live baseline (so the loop's roots fit) but below the
	// baseline plus the scratch buffer (so the buffer must be rejected). Sit the
	// quota midway so neither bound is grazed. An empty loop body charges no extra
	// memory, leaving the scratch buffer as the sole over-budget allocation.
	tight := base + scratch/2
	if tight <= base || tight >= base+scratch {
		t.Fatalf("test setup expects base (%d) < tight (%d) < base+scratch (%d)", base, tight, base+scratch)
	}
	tightScript := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: tight}, source)
	requireCallRuntimeErrorType(t, tightScript, "run", []Value{receiver}, CallOptions{}, runtimeErrorTypeLimit)

	// Sanity: a quota that also fits the scratch buffer admits the loop, proving the
	// rejection above comes from the buffer accounting and not an over-tight
	// baseline. The empty body charges no per-iteration memory, so the scratch
	// buffer the quota now covers is the loop's only allocation beyond the roots.
	roomy := base + scratch + 64*1024
	roomyScript := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: roomy}, source)
	if _, err := roomyScript.Call(context.Background(), "run", []Value{receiver}, CallOptions{}); err != nil {
		t.Fatalf("for loop under a quota that fits the key buffer = %v, want success", err)
	}
}

// TestLoopScratchReservationChargesBodyChecks pins the core of the P2 finding on
// PR #826: a hash-walking loop's sorted key scratch buffer stays live on the Go
// stack while the loop body runs, yet the body's own memory checks measure only
// the execution's reachable roots and so never see that scratch. reserveLoopScratch
// folds the scratch into the baseline every later check observes, and
// releaseLoopScratch removes it again, so a body allocation that breaches
// roots+scratch+body is rejected even though roots+body alone would fit.
func TestLoopScratchReservationChargesBodyChecks(t *testing.T) {
	t.Parallel()

	// The body's per-iteration allocation, modeled as a single value checked while
	// the scratch is held. Sized so roots+body fits the quota but roots+scratch+body
	// does not, isolating the scratch as the deciding term.
	body := largeHashReceiver(4)
	const scratch = 4096

	exec := &Execution{ctx: context.Background(), quota: 1 << 30}
	bodyBytes := exec.estimateMemoryUsage(body)
	exec.memoryQuota = bodyBytes + scratch/2

	// Before any reservation the body allocation fits on its own.
	if err := exec.checkMemoryWith(body); err != nil {
		t.Fatalf("body allocation without held scratch = %v, want success", err)
	}

	// While the scratch is reserved the same check sees roots+scratch+body and
	// rejects, exactly as a body check inside the loop now does.
	delta := exec.reserveLoopScratch(scratch)
	if err := exec.checkMemoryWith(body); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("body allocation while scratch held = %v, want errMemoryQuotaExceeded", err)
	}

	// Releasing the reservation restores the baseline so the body allocation fits
	// again, proving the reservation is balanced and scoped to the loop's lifetime.
	exec.releaseLoopScratch(delta)
	if exec.reservedScratchBytes != 0 {
		t.Fatalf("reservedScratchBytes after release = %d, want 0", exec.reservedScratchBytes)
	}
	if err := exec.checkMemoryWith(body); err != nil {
		t.Fatalf("body allocation after releasing scratch = %v, want success", err)
	}
}

// TestForHashLoopHoldsScratchAcrossBody drives the P2 finding through the real
// `for pair in hash` interpreter path. The sorted key scratch buffer must stay
// reserved while the loop body runs, so a body allocation that is itself smaller
// than the held scratch still overflows a quota tuned to exactly the empty-body
// peak. Before the fix the body's own memory check did not see the scratch, so a
// body smaller than the scratch slipped past and the true peak escaped the quota.
func TestForHashLoopHoldsScratchAcrossBody(t *testing.T) {
	t.Parallel()

	// A receiver large enough that the sorted key buffer is a real heap allocation
	// that dominates the small per-iteration body below.
	const count = 4_000
	receiver := largeHashReceiver(count)
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	emptyBody := `def run(values)
  for pair in values
  end
end`
	// The body allocates a tiny two-element array each iteration -- far smaller than
	// the held scratch buffer -- so only the scratch reservation can push a quota
	// sized to the empty-body peak over the limit.
	allocBody := `def run(values)
  for pair in values
    acc = [0, 0]
    acc
  end
end`

	// emptyPeak is the tightest quota that still admits the empty-body loop: the real
	// resident roots plus the held scratch. Binary search avoids hand-computing every
	// transient the interpreter holds across the iteration.
	emptyPeak := tightestForHashQuota(t, emptyBody, receiver)

	// At exactly that peak the body-allocating loop must overflow, because the tiny
	// body array piles onto the already-maxed roots+scratch baseline. A body smaller
	// than the scratch only trips the quota when the scratch is held through the body,
	// which is the finding.
	tight := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: emptyPeak}, allocBody)
	requireCallRuntimeErrorType(t, tight, "run", []Value{receiver}, CallOptions{}, runtimeErrorTypeLimit)

	// Sanity: widening the quota past the body allocation admits the loop, proving the
	// rejection came from the held scratch plus body and not an unrelated bound.
	roomy := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: emptyPeak + scratch + 64*1024}, allocBody)
	if _, err := roomy.Call(context.Background(), "run", []Value{receiver}, CallOptions{}); err != nil {
		t.Fatalf("body-allocating loop under a roomy quota = %v, want success", err)
	}
}

// tightestForHashQuota binary-searches the smallest MemoryQuotaBytes under which
// run(receiver) still succeeds, so a test can pin a `for` loop's real live peak
// without hand-computing every transient the interpreter holds.
func tightestForHashQuota(t *testing.T, source string, receiver Value) int {
	t.Helper()
	admits := func(quota int) bool {
		script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: quota}, source)
		_, err := script.Call(context.Background(), "run", []Value{receiver}, CallOptions{})
		return err == nil
	}
	lo, hi := 0, 1<<22
	if !admits(hi) {
		t.Fatalf("upper bound quota %d did not admit the loop", hi)
	}
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if admits(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
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

// TestHashTransformValuesFreshScalarHashesTripsMemoryQuota guards the value-slot
// accounting on PR #776 through the accumulator transform_values drives: a block
// returning a fresh hash of scalar values per entry. Each result hash's per-entry
// value slots (len(values)*estimatedValueBytes) are part of the estimator's measure
// of the value, so the conservative accumulator -- which charges every inserted
// value its full est.value footprint -- must count them; a charge that omitted them
// would undercount the build by count*slotsPerResult*slot bytes and let the
// block-produced hashes accumulate past the quota before any check observed the peak.
//
// The test exercises the accumulator directly (the same newHashBuildAccumulator +
// add per entry that transform_values uses) rather than the full builtin, because
// the builtin's post-call check independently rejects an over-quota materialized
// result and would mask whether the accumulator caught the undercount. The entry's
// structural slot (map bucket, key header, value slot) is reserved up front by
// reserveBacking and the receiver key's payload is already in the call-root
// baseline, so add charges only the block-returned value's PAYLOAD beyond its slot
// (the result hash's reachable footprint, including its per-entry value slots).
// That is the single source of truth the accumulator mirrors.
func TestHashTransformValuesFreshScalarHashesTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// The block maps a value to a fresh three-key scalar hash, exactly the
	// transform_values shape the finding called out. The value is freshly allocated
	// and never aliases the empty baseline, so the accumulator charges the result's
	// full payload with no dedup -- the clean case where the omitted value slots
	// would be a pure undercount.
	result := NewHash(map[string]Value{
		"a": NewInt(1),
		"b": NewInt(2),
		"c": NewInt(3),
	})
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	exec.root = newEnv(nil)
	acc := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	if err := acc.add(result); err != nil {
		t.Fatalf("add tripped under an unbounded quota: %v", err)
	}

	// The conservative accumulator's charge for the entry must equal the value's
	// payload beyond its slot (the entry's structural slot is reserved up front by
	// reserveBacking, and the receiver key's payload is in the baseline). The result
	// hash's per-entry value slots are part of est.valuePayload, so a charge that
	// omits them would charge slotsPerResult*estimatedValueBytes too little.
	want := newMemoryEstimator().valuePayload(result)
	if acc.built != want {
		t.Fatalf("accumulator charged %d entry bytes, estimator charges %d (the result hash's value slots must be counted)", acc.built, want)
	}

	// End-to-end sanity: transform_values producing the same fresh scalar hashes is
	// rejected once its accumulated footprint crosses the quota. The receiver entries
	// each map to a three-key hash, so the materialized result dwarfs a tiny quota and
	// the build is bounded rather than escaping.
	receiver := largeHashReceiver(20_000)
	source := `def run(values)
  values.transform_values { |v| { "a": v, "b": v + 1, "c": v + 2 } }
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 16 * 1024}, source)
	requireCallRuntimeErrorType(t, script, "run", []Value{receiver}, CallOptions{}, runtimeErrorTypeLimit)
}

// TestHashMergeConflictBlockMutatesAndReturnsReceiverValueTrips drives the P1
// finding through the real merge code path end to end, not just the accumulator
// in isolation. The bug lived in merge's base-copy loop, which charged each
// receiver entry through the results-only estimator before any conflict block ran.
// That seeded the estimator's seen-set with the receiver's array backings, so a
// conflict block that mutated one of those arrays in place (within its spare
// capacity, keeping the same backing) and returned it -- old[1] = big; old -- was
// then deduplicated against the already-seen backing on its acc.add, charging ~0
// for the freshly stored payload. The merge could grow past the quota one fresh
// payload at a time until the post-call safety net, a sandbox escape.
//
// The fix stops seeding base/argument entries into the estimator; only the
// block-returned conflict result is charged, at its full current footprint. The
// quota here sits above the receiver's own footprint (so the structural projection
// and the receiver copy are admitted) but below that footprint plus the fresh
// block-synthesized payload, so the conflict result must trip the accumulator. The
// payload is created inside the block (not passed as a call root), so it is genuinely
// invisible to the baseline: only charging the returned value's full footprint can
// catch it. Under the pre-fix base-seeded accounting the same quota would have
// wrongly admitted the merge, since the mutated array's backing was already in the
// seen-set and its fresh payload dedup'd to nothing.
func TestHashMergeConflictBlockMutatesAndReturnsReceiverValueTrips(t *testing.T) {
	t.Parallel()

	const payload = 256 * 1024

	// The receiver maps key "x" to a two-element array. The conflict block stores a
	// fresh large string into slot 1 (in place, keeping the same backing) and returns
	// the array, so the result aliases the receiver-owned backing that the base copy
	// would have seeded into the estimator.
	receiver := NewHash(map[string]Value{"x": NewArray([]Value{NewInt(0), NewInt(0)})})
	arg := NewHash(map[string]Value{"x": NewInt(1)})

	// Measure the live footprint the merge holds before producing any block result:
	// exec's roots plus the call roots (receiver, the argument hash). The fresh payload
	// is synthesized inside the block, so it is deliberately absent from this baseline.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(receiver, []Value{arg}, nil, NewNil())

	// A quota above the receiver's footprint plus the merge's one-entry output map, so
	// the structural projection passes and the receiver array fits, but well below that
	// plus the fresh payload, so charging the conflict result's full footprint trips.
	outputStructure := estimatedValueBytes + estimatedMapBaseBytes +
		(estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes)
	quota := liveWithRoots + outputStructure + payload/2

	source := `def run(receiver, other)
  receiver.merge(other) { |k, old, new| old[1] = "".ljust(262144, "z"); old }
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: quota}, source)
	requireCallRuntimeErrorType(t, script, "run", []Value{receiver, arg}, CallOptions{}, runtimeErrorTypeLimit)
}

// TestHashMergeReservesUnionBackingNotBaseLen pins the P1 finding on PR #776
// (members_hash.go:655): a block merge that inserts many non-conflicting argument
// keys grows the output map from len(base) up to the distinct union before any
// conflict block result is charged. The accumulator must reserve that union's
// backing, not just len(base), or an early conflict result is checked against a
// backing far smaller than the one actually live -- so backing + an early result
// can exceed the quota until a later post-call check observes the peak.
//
// The test drives the accumulator exactly as merge does (reserveBacking +
// reserveScratch, then add per conflict result) under two reservations against one
// accumulator each. Reserving the union (the fix) rejects the first conflict
// result, because the grown backing plus the result overflows the quota. Reserving
// only len(base) (the pre-fix bug) wrongly admits it, since the under-reserved
// backing leaves headroom the live map has already consumed. The gap between the
// two is exactly the (union - len(base)) slots the non-conflict additions grow.
func TestHashMergeReservesUnionBackingNotBaseLen(t *testing.T) {
	t.Parallel()

	const baseLen = 1
	const nonConflictKeys = 4_000
	const unionLen = baseLen + nonConflictKeys

	// The conflict block returns a fresh string per collision. Size it so that, on
	// the union-sized backing, backing + the first result overflows the quota, while
	// on a len(base)-sized backing the same result still fits.
	const resultPayload = 32 * 1024
	result := NewString(strings.Repeat("z", resultPayload))

	// Learn the accumulator baseline (call roots + empty map) under no real pressure,
	// matching what merge snapshots before reserving any backing.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1}
	probe.root = newEnv(nil)
	probeAcc := newHashBuildAccumulator(probe, NewNil(), nil, nil, NewNil())
	base := probeAcc.base
	resultBytes := newMemoryEstimator().valuePayload(result)

	// Size the quota to fit the union backing plus the first result short by one byte,
	// so reserving the union and then adding the result trips, while reserving only
	// len(base) leaves room the union-grown map has actually consumed.
	quota := base + unionLen*estimatedMapEntryStructuralBytes + resultBytes - 1

	// Sanity: a len(base)-sized backing plus the same result must fit the quota, so
	// the pre-fix reservation would wrongly admit the result.
	if base+baseLen*estimatedMapEntryStructuralBytes+resultBytes > quota {
		t.Fatalf("test setup expects base-len backing + result (%d) to fit the quota (%d)",
			base+baseLen*estimatedMapEntryStructuralBytes+resultBytes, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	exec.root = newEnv(nil)

	// The fix: reserving the union backing, then the first conflict result trips,
	// because the grown backing already consumed the quota's headroom.
	fixed := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	if err := fixed.reserveBacking(unionLen); err != nil {
		t.Fatalf("reserving the union backing under a quota sized to hold it tripped early: %v", err)
	}
	if err := fixed.add(result); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("union-backed accumulator admitted the first conflict result, want rejection: %v", err)
	}

	// The pre-fix bug: reserving only len(base) leaves headroom the union-grown map
	// has already consumed, so the same result is wrongly admitted.
	buggy := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	if err := buggy.reserveBacking(baseLen); err != nil {
		t.Fatalf("reserving the base-len backing tripped early: %v", err)
	}
	if err := buggy.add(result); err != nil {
		t.Fatalf("base-len-backed accumulator rejected the first result, want it to wrongly admit (proving the under-reservation): %v", err)
	}
}

// TestHashMergeNonConflictGrowthWithEarlyConflictTrips drives the same P1 finding
// through the real merge code path end to end. The argument hash adds thousands of
// non-conflicting keys (growing the output map well past len(base)) plus one
// conflicting key ("x") whose block returns a fresh payload. The union backing is
// reserved up front (before the copy/additions loops), so iteration order is
// irrelevant: by the time the conflict block runs, the full union backing is
// already charged, and the grown backing plus that result overflows a quota that
// fits the union backing alone, so the merge is rejected. Reserving only len(base)
// (the pre-fix bug) would have let this same merge materialize the full union map
// plus the result before the post-call check.
func TestHashMergeNonConflictGrowthWithEarlyConflictTrips(t *testing.T) {
	t.Parallel()

	const nonConflictKeys = 4_000
	const conflictPayload = 64 * 1024

	// The receiver holds one key the argument also carries (the conflict), so the
	// block runs exactly once. base = 1 entry; the union grows to 1 + nonConflictKeys.
	receiver := NewHash(map[string]Value{"x": NewInt(0)})

	addition := make(map[string]Value, nonConflictKeys+1)
	addition["x"] = NewInt(1)
	for i := range nonConflictKeys {
		addition["k"+strconv.Itoa(i)] = NewInt(int64(i))
	}
	arg := NewHash(addition)
	args := []Value{arg}

	unionLen := 1 + nonConflictKeys
	scratch := mergeSortScratchBytes(args)

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	probe.root = newEnv(nil)
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(receiver, args, nil, NewNil())
	emptyMap := estimatedValueBytes + estimatedMapBaseBytes
	unionBacking := unionLen * estimatedMapEntryStructuralBytes
	resultBytes := newMemoryEstimator().stringPayloadSize(strings.Repeat("z", conflictPayload))

	// A quota that fits the live roots, the full union backing, and the scratch, but
	// not the conflict block's fresh result on top. With the union reserved, the
	// result trips at acc.add; reserving only len(base) would leave (union-1) slots of
	// phantom headroom and admit it.
	quota := liveWithRoots + emptyMap + unionBacking + scratch + resultBytes/2

	// Sanity: the conflict result alone exceeds the headroom left after the union
	// backing, so the rejection is genuinely the union-reservation closing the gap.
	if resultBytes <= quota-(liveWithRoots+emptyMap+unionBacking+scratch) {
		t.Fatalf("test setup expects the result (%d) to exceed the post-union headroom (%d)",
			resultBytes, quota-(liveWithRoots+emptyMap+unionBacking+scratch))
	}

	source := `def run(receiver, other)
  receiver.merge(other) { |k, old, new| "".ljust(65536, "z") }
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: quota}, source)
	requireCallRuntimeErrorType(t, script, "run", []Value{receiver, arg}, CallOptions{}, runtimeErrorTypeLimit)
}

// sharedValueBlock builds a conflict block (its params are ignored) whose body
// returns the same captured value on every invocation. Because each call yields a
// value backed by the identical pointer, the merge accumulator's results-only
// estimator deduplicates it to a single payload charge across every conflict,
// mirroring sharedArrayBlockValue for the array accumulator.
func sharedValueBlock(shared Value) Value {
	pos := Position{Line: 1, Column: 1}
	env := newEnv(nil)
	env.Define("shared", shared)
	body := []Statement{&ExprStmt{Expr: &Identifier{Name: "shared", Position: pos}, Position: pos}}
	return NewBlock(nil, body, env)
}

// TestHashMergeOverlappingBlockExactUnionFitsLooseBoundWouldReject pins the P2
// finding on PR #776 (members_hash.go:613): an overlapping merge with a conflict
// block must reserve the EXACT distinct union as the accumulator's output backing,
// not the loose len(base)+sum(arg lens) upper bound. The loose bound over-counts
// every overlapping key, so reserving it holds phantom slots the result map never
// allocates and falsely rejects a merge whose true union plus its block results
// fits the quota.
//
// The receiver and the argument hold the same keys but are distinct map objects, so
// every key conflicts: the union is exactly len(base) while the loose bound is
// 2*len(base). The conflict block returns a single shared large string (counted
// once, since every call yields the same backing). The quota is sized so the loose
// bound's structural backing alone still passes the up-front projection -- the
// regime where the buggy code leaves projectedEntries at the loose bound -- but the
// loose backing plus the shared block result overflows it, while the exact union
// backing plus the same result fits exactly. With the fix (reserve the exact union)
// the merge succeeds; reserving the loose bound would wrongly reject it.
func TestHashMergeOverlappingBlockExactUnionFitsLooseBoundWouldReject(t *testing.T) {
	t.Parallel()

	const count = 8_000

	// The conflict block returns the same shared string on every collision, so its
	// payload is charged once through the accumulator's results-only estimator no
	// matter how many keys collide. The payload is sized well below the gap between
	// the loose and exact backings (count*estimatedMapEntryStructuralBytes) so the
	// fixed (exact-union) reservation plus this result leaves room for the
	// interpreter's own per-step memory checks, while the loose reservation plus the
	// same result overflows. Keeping it comfortably inside that gap makes the test
	// robust to the exact size of those interpreter checks.
	resultPayload := 64 * 1024
	shared := NewString(strings.Repeat("z", resultPayload))
	block := sharedValueBlock(shared)

	receiver := largeHashReceiver(count)
	arg := largeHashReceiver(count)
	args := []Value{arg}

	// Mirror the merge accumulator's baseline by driving the real block value through
	// the probe: live call roots (receiver, arg, and the block whose env captures the
	// shared result) plus the empty output map.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	probe.root = newEnv(nil)
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(receiver, args, nil, block)
	emptyMap := estimatedValueBytes + estimatedMapBaseBytes
	scratch := mergeSortScratchBytes(args)
	exactBacking := count * estimatedMapEntryStructuralBytes
	looseBacking := 2 * count * estimatedMapEntryStructuralBytes
	resultBytes := newMemoryEstimator().valuePayload(shared)

	// Size the quota to exactly the loose bound's structural backing plus scratch, the
	// boundary where the buggy loose-first projection still passes the up-front check
	// (so it keeps the loose bound rather than recomputing the exact union). With the
	// loose bound reserved, charging the shared result then overflows; with the exact
	// union reserved, the same result stays within the quota.
	quota := liveWithRoots + emptyMap + looseBacking + scratch

	// Sanity: reserving the loose bound and then charging the shared result overflows
	// the quota, so the buggy reservation would wrongly reject this merge.
	if liveWithRoots+emptyMap+looseBacking+scratch+resultBytes <= quota {
		t.Fatalf("test setup expects loose backing + result (%d) to exceed the quota (%d)",
			liveWithRoots+emptyMap+looseBacking+scratch+resultBytes, quota)
	}
	// Sanity: the exact union backing plus the result leaves headroom below the quota
	// (the loose/exact gap, count*estimatedMapEntryStructuralBytes, must exceed the
	// result so the fix genuinely admits the merge with room for interpreter checks).
	if liveWithRoots+emptyMap+exactBacking+scratch+resultBytes >= quota {
		t.Fatalf("test setup expects exact backing + result (%d) to stay below the quota (%d)",
			liveWithRoots+emptyMap+exactBacking+scratch+resultBytes, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	exec.root = newEnv(nil)
	got, err := callHashMember(t, exec, receiver, "merge", args, block)
	if err != nil {
		t.Fatalf("overlapping merge whose exact union fits the quota = %v, want success (the loose bound would wrongly reject)", err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != count {
		t.Fatalf("overlapping merge produced %v with %d entries, want a hash with %d", got.Kind(), len(got.Hash()), count)
	}
}
