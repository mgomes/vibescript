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

// singleParamPairBlock builds a block declaring exactly one positional parameter
// whose body references that parameter, mirroring the |pair| form Hash#each
// collapses entries into. The single positional parameter makes
// wantsCollapsedPair true (so the iterator allocates a [key, value] pair per
// entry), and the body captures nothing, so blockCanReuseEnv stays true -- the
// regime where the previous iteration's pair lingers in the reused env while the
// next pair is allocated.
func singleParamPairBlock() Value {
	pos := Position{Line: 1, Column: 1}
	params := []Param{{Name: "pair", Kind: ParamNormal}}
	body := []Statement{&ExprStmt{Expr: &Identifier{Name: "pair", Position: pos}, Position: pos}}
	return NewBlock(params, body, newEnv(nil))
}

// TestHashEachEmptySingleParamFitsCallRoots pins the first P2 finding on PR #808:
// an empty receiver iterated by a single-parameter block allocates no [key, value]
// pair, so the iterator must reserve no per-entry pair bytes. A quota sized to the
// bare call roots (an empty receiver heaps no sorted-key buffer, so the roots are
// the only live footprint) must admit {}.each do |pair| ... end. Before the fix the
// iterator charged collapsedPairBytes even with zero entries, so this exact-roots
// quota wrongly rejected the empty-hash walk.
func TestHashEachEmptySingleParamFitsCallRoots(t *testing.T) {
	t.Parallel()

	receiver := NewHash(map[string]Value{})
	block := singleParamPairBlock()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	if scratch := sortedKeyBufferBytes(0); scratch != 0 {
		t.Fatalf("empty receiver expects no sorted-key buffer, got %d bytes", scratch)
	}

	// A quota of exactly the call roots: no pair, no scratch, no output map. With the
	// fix the empty walk fits; charging even one collapsedPairBytes here would push the
	// projection one pair over and reject.
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: roots}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("{}.each do |pair| at the exact call-roots quota %d = %v, want success", roots, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != 0 {
		t.Fatalf("{}.each returned %v with %d entries, want the empty hash receiver", got.Kind(), len(got.Hash()))
	}

	// Guard: a quota one byte below the call roots still rejects, proving the success
	// above comes from the roots fitting exactly and not from an unbounded short
	// circuit or slack in the projection.
	if roots > 0 {
		tight := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: roots - 1}
		if _, err := callHashMember(t, tight, receiver, "each", nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
			t.Fatalf("{}.each one byte below the call roots = %v, want errMemoryQuotaExceeded", err)
		}
	}
}

// TestHashEachSingleEntrySingleParamReservesOnePair pins the P2 finding on PR
// #808's second review pass: the two-live-pairs overlap needs a previous iteration
// to exist, so a single-entry receiver yields exactly ONE live pair and the
// iterator's preflight must reserve one pair, not two. A quota one byte below the
// roots-plus-two-pairs reservation still leaves room for the real single-pair peak,
// so the walk must succeed; before the fix the preflight always reserved two pairs
// for any non-empty receiver and rejected this quota up front even though execution
// fits comfortably.
func TestHashEachSingleEntrySingleParamReservesOnePair(t *testing.T) {
	t.Parallel()

	receiver := NewHash(map[string]Value{"k0": NewInt(0)})
	block := singleParamPairBlock()
	if !blockCanReuseEnv(valueBlock(block)) {
		t.Fatal("test setup expects the single-parameter block to reuse its environment")
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	if scratch := sortedKeyBufferBytes(1); scratch != 0 {
		t.Fatalf("single-entry receiver expects no heap sorted-key buffer, got %d bytes", scratch)
	}

	// One byte below the roots-plus-two-pairs reservation. The old preflight reserved
	// two pairs for any non-empty receiver, so used == roots+2*pair > this quota and
	// it rejected. The fix reserves one pair for a single entry, and the real peak
	// (one pair plus the block's small per-call binding overhead) fits well under
	// roots+2*pair, so the walk must now succeed.
	belowTwoPairQuota := roots + 2*collapsedPairBytes - 1
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: belowTwoPairQuota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("single-entry each one byte below the two-pair reservation (quota %d) = %v, want success", belowTwoPairQuota, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != 1 {
		t.Fatalf("single-entry each returned %v with %d entries, want the one-entry receiver", got.Kind(), len(got.Hash()))
	}

	// Guard: a quota below even the single-entry call roots still rejects, proving the
	// success above is not an unbounded short circuit in the projection.
	if roots > 0 {
		tight := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: roots - 1}
		if _, err := callHashMember(t, tight, receiver, "each", nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
			t.Fatalf("single-entry each one byte below the call roots = %v, want errMemoryQuotaExceeded", err)
		}
	}
}

// TestHashEachSingleParamReservesTwoLivePairs pins the second P2 finding on PR
// #808: a single-parameter block that reuses its environment briefly holds two
// [key, value] pairs live at once -- the previous iteration's pair stays bound in
// the reused env until runner.call resets it, but the next pair is allocated before
// that reset. The iterator must therefore reserve two pairs, not one. A quota sized
// to the call roots, the sorted-key scratch, and exactly ONE pair must trip; a quota
// that also fits the second pair must admit the walk. Before the fix only one pair
// was reserved, so the one-pair quota wrongly passed even though two pairs overlap.
func TestHashEachSingleParamReservesTwoLivePairs(t *testing.T) {
	t.Parallel()

	const count = 50_000
	receiver := largeHashReceiver(count)
	block := singleParamPairBlock()
	if !blockCanReuseEnv(valueBlock(block)) {
		t.Fatal("test setup expects the single-parameter block to reuse its environment")
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	// Room for the roots, the scratch, and exactly one pair. The loop keeps the old
	// and new pair live simultaneously, so reserving two pairs (the fix) rejects this
	// one-pair quota up front.
	onePairQuota := roots + scratch + collapsedPairBytes
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: onePairQuota}
	if _, err := callHashMember(t, exec, receiver, "each", nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("single-parameter each at a one-pair quota %d = %v, want errMemoryQuotaExceeded (two pairs are briefly live)", onePairQuota, err)
	}

	// Room for both live pairs admits the walk, proving the rejection above comes from
	// the two-pair reservation and not an over-tight baseline. The block body runs no
	// allocating statement, so the two reserved pairs plus a small slack for the
	// interpreter's own per-step checks bound the true peak.
	twoPairQuota := roots + scratch + 2*collapsedPairBytes + 64*1024
	exec = &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: twoPairQuota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("single-parameter each at a two-pair quota %d = %v, want success", twoPairQuota, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != count {
		t.Fatalf("single-parameter each returned %v with %d entries, want the %d-entry receiver", got.Kind(), len(got.Hash()), count)
	}
}

// destructurePairBlock builds a block declaring a single destructuring parameter
// |(k, v)|, the form Hash#each collapses each entry into and the block immediately
// unpacks. The lone positional parameter makes wantsCollapsedPair true, but its
// Target is a DestructureTarget rather than a named identifier, so callBlock routes
// it through bindBlockParamTarget. Even though the bind stores no pair array in the
// reused env, the loop reuses a single pairArg slot, so the next iteration's pair
// is allocated while pairArg still holds the previous one: two pairs overlap just
// as they do for the |pair| form. The body captures nothing, so blockCanReuseEnv
// stays true, the same env-reuse regime the |pair| form exercises.
func destructurePairBlock() Value {
	pos := Position{Line: 1, Column: 1}
	target := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "k", Position: pos}},
			{Target: &Identifier{Name: "v", Position: pos}},
		},
	}
	params := []Param{{Kind: ParamNormal, Target: target}}
	body := []Statement{
		&ExprStmt{Expr: &Identifier{Name: "k", Position: pos}, Position: pos},
		&ExprStmt{Expr: &Identifier{Name: "v", Position: pos}, Position: pos},
	}
	return NewBlock(params, body, newEnv(nil))
}

// destructureRestPairBlock builds a block declaring a single destructuring
// parameter with a rest target |(k, *rest)| and an EMPTY body. Binding the
// collapsed [key, value] pair routes through AssignDestructure, which allocates a
// fresh array for the rest target (here holding the value not claimed by the fixed
// k target) and binds it into the reused env, so that rest array is live memory on
// top of the pair. The empty body runs no allocating statement and therefore no
// in-block memory check, so only the iterator's preflight reservation guards the
// rest array -- the regime the P2 finding flagged.
func destructureRestPairBlock() Value {
	pos := Position{Line: 1, Column: 1}
	target := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "k", Position: pos}},
			{Target: &Identifier{Name: "rest", Position: pos}, Rest: true},
		},
	}
	params := []Param{{Kind: ParamNormal, Target: target}}
	return NewBlock(params, nil, newEnv(nil))
}

// nestedRestDestructureBlock builds a block declaring a single destructuring
// parameter whose second element is itself a destructure with a rest target
// |(k, (head, *tail))| and an EMPTY body. Over a hash whose values are arrays, the
// collapsed [key, value] pair binds k to the key and destructures the value array
// into head and tail, so AssignDestructure allocates a fresh rest array holding all
// but the first value element. That rest is bounded by the value's length, not the
// two-element pair, so a fixed pair-sized reservation cannot charge it. The empty
// body runs no allocating statement, so only the iterator's preflight reservation
// guards the rest array before it is materialized -- the regime the nested-rest P2
// finding flagged.
func nestedRestDestructureBlock() Value {
	pos := Position{Line: 1, Column: 1}
	target := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "k", Position: pos}},
			{Target: &DestructureTarget{
				Position: pos,
				Elements: []DestructureElement{
					{Target: &Identifier{Name: "head", Position: pos}},
					{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
				},
			}},
		},
	}
	params := []Param{{Kind: ParamNormal, Target: target}}
	return NewBlock(params, nil, newEnv(nil))
}

// restTargetDestructureBlock builds a block whose single destructuring parameter is
// a one-element list whose only element is a rest target that is ITSELF a destructure
// with its own rest |(*(head, *tail))| and an EMPTY body. AssignDestructure first
// collects the unclaimed slice into a fresh outer rest array, then -- because the
// rest target is a destructure -- recurses into that array and allocates a SECOND,
// nested tail rest array. A reservation that charges only the outer rest array (the
// pre-fix behavior, which skipped the rest target) under-counts by that nested array;
// with an empty body no in-block check guards it, so the nested allocation would
// escape the hash-walk bound. This shape is the 2-deep nested-rest case the P2
// finding flagged.
func restTargetDestructureBlock() Value {
	pos := Position{Line: 1, Column: 1}
	target := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &DestructureTarget{
				Position: pos,
				Elements: []DestructureElement{
					{Target: &Identifier{Name: "head", Position: pos}},
					{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
				},
			}, Rest: true},
		},
	}
	params := []Param{{Kind: ParamNormal, Target: target}}
	return NewBlock(params, nil, newEnv(nil))
}

// deepRestTargetDestructureBlock builds a block whose single destructuring parameter
// nests a rest target that is a destructure whose own rest target is again a
// destructure |(*(*(a, *b)))| with an EMPTY body. AssignDestructure allocates a rest
// array at all three depths, so a reservation that stops short of the deepest rest
// under-counts by the innermost array. This is the 3-deep nested-rest case proving the
// accounting is recursive over arbitrary depth, not patched per shape.
func deepRestTargetDestructureBlock() Value {
	pos := Position{Line: 1, Column: 1}
	target := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &DestructureTarget{
				Position: pos,
				Elements: []DestructureElement{
					{Target: &DestructureTarget{
						Position: pos,
						Elements: []DestructureElement{
							{Target: &Identifier{Name: "a", Position: pos}},
							{Target: &Identifier{Name: "b", Position: pos}, Rest: true},
						},
					}, Rest: true},
				},
			}, Rest: true},
		},
	}
	params := []Param{{Kind: ParamNormal, Target: target}}
	return NewBlock(params, nil, newEnv(nil))
}

// largeArrayValueHashReceiver builds a hash with count entries whose values are
// arrays of valueLen ints. It backs the nested-rest memory test: destructuring the
// value with a rest target collects valueLen-1 of those ints into a fresh array,
// giving the preflight a rest allocation bounded by valueLen rather than the pair.
func largeArrayValueHashReceiver(count, valueLen int) Value {
	entries := make(map[string]Value, count)
	for i := range count {
		elems := make([]Value, valueLen)
		for j := range valueLen {
			elems[j] = NewInt(int64(j))
		}
		entries["k"+strconv.Itoa(i)] = NewArray(elems)
	}
	return NewHash(entries)
}

// twoParamPairBlock builds a block declaring two positional parameters |k, v|, the
// auto-splat form Hash#each yields key and value separately into. Two positional
// parameters make wantsCollapsedPair false, so the iterator allocates no pair array
// at all and must reserve zero per-entry bytes.
func twoParamPairBlock() Value {
	pos := Position{Line: 1, Column: 1}
	params := []Param{
		{Name: "k", Kind: ParamNormal},
		{Name: "v", Kind: ParamNormal},
	}
	body := []Statement{
		&ExprStmt{Expr: &Identifier{Name: "k", Position: pos}, Position: pos},
		&ExprStmt{Expr: &Identifier{Name: "v", Position: pos}, Position: pos},
	}
	return NewBlock(params, body, newEnv(nil))
}

// singleParamKeyBlock builds a block declaring one positional parameter for the
// each_key / each_value walks, which bind the key or value directly and never
// collapse a pair. It mirrors singleParamPairBlock's shape so the iterator's arity
// inspection sees a single positional parameter, proving those walks reserve no
// pair bytes regardless.
func singleParamKeyBlock() Value {
	pos := Position{Line: 1, Column: 1}
	params := []Param{{Name: "x", Kind: ParamNormal}}
	body := []Statement{&ExprStmt{Expr: &Identifier{Name: "x", Position: pos}, Position: pos}}
	return NewBlock(params, body, newEnv(nil))
}

// TestHashEachDestructureParamReservesTwoLivePairs pins the P2 finding on PR
// #808: a single destructuring parameter |(k, v)| collapses each entry into a pair
// allocated through the loop's reused pairArg slot, so the next iteration's pair is
// built while pairArg still references the previous one -- two pair arrays are
// briefly live, exactly as for the |pair| whole-pair binding. An earlier fix
// wrongly reserved only one pair for the destructuring shape on the theory that the
// bind released the previous pair; the reused slot keeps two live regardless. A
// quota sized to the call roots, the sorted-key scratch, and exactly ONE pair must
// trip; a quota that also fits the second pair must admit the walk.
func TestHashEachDestructureParamReservesTwoLivePairs(t *testing.T) {
	t.Parallel()

	const count = 50_000
	receiver := largeHashReceiver(count)
	block := destructurePairBlock()
	if valueBlock(block).Params[0].Target == nil {
		t.Fatal("test setup expects a |(k, v)| destructuring parameter (non-nil Target)")
	}
	if !blockCanReuseEnv(valueBlock(block)) {
		t.Fatal("test setup expects the destructuring block to reuse its environment")
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	// Room for the roots, the scratch, and exactly one pair. The reused pairArg slot
	// keeps the old and new pair live simultaneously, so reserving two pairs rejects
	// this one-pair quota up front. The earlier one-pair reservation wrongly admitted
	// it even though two pairs overlap.
	onePairQuota := roots + scratch + collapsedPairBytes
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: onePairQuota}
	if _, err := callHashMember(t, exec, receiver, "each", nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("destructuring each at a one-pair quota %d = %v, want errMemoryQuotaExceeded (two pairs are briefly live)", onePairQuota, err)
	}

	// Room for both live pairs admits the walk, proving the rejection above comes from
	// the two-pair reservation and not an over-tight baseline. The block body runs no
	// allocating statement, so the two reserved pairs plus a small slack for the
	// interpreter's own per-step checks bound the true peak.
	twoPairQuota := roots + scratch + 2*collapsedPairBytes + 64*1024
	exec = &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: twoPairQuota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("destructuring each at a two-pair quota %d = %v, want success", twoPairQuota, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != count {
		t.Fatalf("destructuring each returned %v with %d entries, want the %d-entry receiver", got.Kind(), len(got.Hash()), count)
	}
}

// TestHashEachDestructureRestReservesRestArrays pins the P2 finding on PR #808:
// a single destructuring parameter with a rest target |(k, *rest)| makes
// AssignDestructure allocate a fresh array for the collected elements, so that rest
// array is live memory the iterator must charge on top of the two [key, value]
// pairs the reused pairArg slot keeps live. The empty block body runs no allocating
// statement and therefore no in-block memory check, so only the preflight
// reservation guards the rest array. The rest array is built inside callBlock,
// after runner.call's resetForBlockCall has already cleared the previous
// iteration's rest binding, so at most ONE rest array is ever live -- unlike the
// pair arrays, the previous rest never overlaps the next one. A quota that admits
// the call roots, the sorted-key scratch, and the two live pairs -- but not the
// rest array -- must trip; a quota that fits one rest array (but not two) must
// admit the walk. The second guard below catches an over-reserved second rest
// array.
func TestHashEachDestructureRestReservesRestArrays(t *testing.T) {
	t.Parallel()

	const count = 50_000
	receiver := largeHashReceiver(count)
	block := destructureRestPairBlock()
	if valueBlock(block).Params[0].Target == nil {
		t.Fatal("test setup expects a |(k, *rest)| destructuring parameter (non-nil Target)")
	}
	if !blockCanReuseEnv(valueBlock(block)) {
		t.Fatal("test setup expects the rest-destructuring block to reuse its environment")
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	// |(k, *rest)| binds the lone fixed target k, so the rest array holds the one
	// remaining pair element (the value). At most one such rest array is live: the
	// previous iteration's rest binding is cleared by resetForBlockCall before the next
	// rest array is allocated inside callBlock.
	const fixedTargets = 1
	restSlots := collapsedPairElements - fixedTargets
	oneRestBytes := restArrayBytes(restSlots)
	if oneRestBytes <= 0 {
		t.Fatalf("rest reservation must be positive, got %d", oneRestBytes)
	}

	// A quota sized to the roots, the scratch, and the two live pairs, but not the
	// rest array. Without charging the rest the preflight would reserve only the two
	// pairs and wrongly admit the walk while the rest allocation went uncharged.
	// Reserving the rest array makes the projection exceed this quota and reject.
	pairsOnlyQuota := roots + scratch + 2*collapsedPairBytes
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: pairsOnlyQuota}
	if _, err := callHashMember(t, exec, receiver, "each", nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("rest-destructuring each at a pairs-only quota %d = %v, want errMemoryQuotaExceeded (the rest array is uncharged)", pairsOnlyQuota, err)
	}

	// Room for the roots, scratch, two pairs, and exactly ONE rest array admits the
	// walk, proving the rejection above comes from charging the rest array and not an
	// over-tight baseline. This quota is one byte below what a second rest array would
	// need, so a doubled rest reservation would wrongly reject it. The empty block body
	// runs no allocating statement, so the two reserved pairs plus one rest array bound
	// the true peak.
	oneRestQuota := roots + scratch + 2*collapsedPairBytes + oneRestBytes
	exec = &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: oneRestQuota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("rest-destructuring each at a two-pair-one-rest quota %d = %v, want success (a second rest array must not be reserved)", oneRestQuota, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != count {
		t.Fatalf("rest-destructuring each returned %v with %d entries, want the %d-entry receiver", got.Kind(), len(got.Hash()), count)
	}
}

// TestHashEachNestedDestructureRestReservesRestArrays pins the nested-rest P2
// finding on PR #808: a single destructuring parameter whose nested element has a
// rest target |(k, (head, *tail))| collects an arbitrarily large slice of the hash
// value it destructures, not just the two-element pair. The earlier preflight only
// scanned top-level elements, treated the nested destructure as one fixed slot, and
// reserved nothing for the nested rest, so a pair-sized quota wrongly admitted the
// walk and the rest arrays went uncharged until evalStatements' post-bind check.
// The fix sizes the reservation from the actual entries. As with a top-level rest,
// the nested rest array is built inside callBlock after resetForBlockCall has
// cleared the previous binding, so at most ONE nested rest array is live at a time.
// A quota that fits the call roots, the sorted-key scratch, and the two live pairs
// the reused pairArg slot holds -- but not the nested rest array -- must trip; a
// quota that fits one nested rest array (but not two) must admit the walk.
func TestHashEachNestedDestructureRestReservesRestArrays(t *testing.T) {
	t.Parallel()

	const count = 20_000
	const valueLen = 64
	receiver := largeArrayValueHashReceiver(count, valueLen)
	block := nestedRestDestructureBlock()
	if valueBlock(block).Params[0].Target == nil {
		t.Fatal("test setup expects a |(k, (head, *tail))| destructuring parameter (non-nil Target)")
	}
	if !blockCanReuseEnv(valueBlock(block)) {
		t.Fatal("test setup expects the nested-rest block to reuse its environment")
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	// The nested destructure binds head to the first value element and collects the
	// remaining valueLen-1 into tail's fresh array. At most one such rest array is live:
	// the previous iteration's tail binding is cleared by resetForBlockCall before the
	// next nested rest array is allocated inside callBlock.
	restSlots := valueLen - 1
	oneRestBytes := restArrayBytes(restSlots)
	if oneRestBytes <= 0 {
		t.Fatalf("nested rest reservation must be positive, got %d", oneRestBytes)
	}

	// A quota sized to the roots, the scratch, and the two live pairs, but not the
	// nested rest array. Without charging the nested rest the preflight would reserve
	// only the pairs and wrongly admit the walk while the rest allocation went
	// uncharged. Sizing the reservation from the entries makes the projection exceed
	// this quota and reject.
	pairsOnlyQuota := roots + scratch + 2*collapsedPairBytes
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: pairsOnlyQuota}
	if _, err := callHashMember(t, exec, receiver, "each", nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("nested-rest each at a pairs-only quota %d = %v, want errMemoryQuotaExceeded (the nested rest array is uncharged)", pairsOnlyQuota, err)
	}

	// Room for the roots, scratch, two pairs, and exactly ONE nested rest array admits
	// the walk, proving the rejection above comes from charging the nested rest array
	// and not an over-tight baseline. This quota is one byte below what a second nested
	// rest array would need, so a doubled rest reservation would wrongly reject it. The
	// empty block body runs no allocating statement, so the two reserved pairs plus one
	// rest array bound the true peak.
	oneRestQuota := roots + scratch + 2*collapsedPairBytes + oneRestBytes
	exec = &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: oneRestQuota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("nested-rest each at a one-pair-one-rest quota %d = %v, want success (a second nested rest array must not be reserved)", oneRestQuota, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != count {
		t.Fatalf("nested-rest each returned %v with %d entries, want the %d-entry receiver", got.Kind(), len(got.Hash()), count)
	}
}

// TestHashEachRestTargetDestructureReservesNestedRestArrays pins the deeper P2
// finding on PR #808: when the rest TARGET is itself a destructure with its own rest
// |(*(head, *tail))|, AssignDestructure does not merely bind the already-charged
// outer rest array -- it recurses into that target and allocates a SECOND, nested
// rest array. The earlier preflight charged only the outer rest array and skipped the
// rest target, so a quota that fits the outer rest but not the nested one wrongly
// admitted the walk and the nested allocation escaped the hash-walk bound (the empty
// body runs no in-block check). The fix makes the rest-allocation accounting fully
// recursive over arbitrary destructure nesting, so a rest target that is a destructure
// charges a rest array at every depth. As with the other rest forms, only ONE nested
// rest array is live at a time: resetForBlockCall clears the previous binding before
// the next is allocated. A quota that fits the call roots, the sorted-key scratch, the
// two live pairs, and the outer rest array -- but not the nested one -- must trip; a
// quota that additionally fits the nested rest array must admit the walk.
func TestHashEachRestTargetDestructureReservesNestedRestArrays(t *testing.T) {
	t.Parallel()

	const count = 50_000
	receiver := largeHashReceiver(count)
	block := restTargetDestructureBlock()
	if valueBlock(block).Params[0].Target == nil {
		t.Fatal("test setup expects a |(*(head, *tail))| destructuring parameter (non-nil Target)")
	}
	if !blockCanReuseEnv(valueBlock(block)) {
		t.Fatal("test setup expects the rest-target-destructure block to reuse its environment")
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	// The outer rest collects the whole two-element pair; recursing into its
	// destructure target binds head to the first element and collects the remaining
	// one into the nested tail array.
	outerRestBytes := restArrayBytes(collapsedPairElements)
	nestedRestBytes := restArrayBytes(collapsedPairElements - 1)
	if outerRestBytes <= 0 || nestedRestBytes <= 0 {
		t.Fatalf("rest reservations must be positive, got outer=%d nested=%d", outerRestBytes, nestedRestBytes)
	}

	// A quota sized to the roots, scratch, two pairs, and only the OUTER rest array --
	// the pre-fix reservation, which skipped the rest target. With the nested rest
	// array now charged the projection exceeds this quota and rejects; before the fix
	// it was wrongly admitted and the nested allocation escaped.
	outerRestOnlyQuota := roots + scratch + 2*collapsedPairBytes + outerRestBytes
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: outerRestOnlyQuota}
	if _, err := callHashMember(t, exec, receiver, "each", nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("rest-target-destructure each at an outer-rest-only quota %d = %v, want errMemoryQuotaExceeded (the nested rest array is uncharged)", outerRestOnlyQuota, err)
	}

	// Room for the roots, scratch, two pairs, and BOTH the outer and nested rest arrays
	// admits the walk, proving the rejection above comes from charging the nested rest
	// array and not an over-tight baseline. This quota is one byte below what a second
	// copy of either rest array would need, so over-reserving would wrongly reject it.
	bothRestQuota := roots + scratch + 2*collapsedPairBytes + outerRestBytes + nestedRestBytes
	exec = &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: bothRestQuota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("rest-target-destructure each at a two-pair-both-rest quota %d = %v, want success (a second rest array must not be reserved)", bothRestQuota, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != count {
		t.Fatalf("rest-target-destructure each returned %v with %d entries, want the %d-entry receiver", got.Kind(), len(got.Hash()), count)
	}
}

// TestHashEachDeepRestTargetDestructureReservesEveryDepth extends the rest-target P2
// regression one level deeper: a rest target whose own rest target is again a
// destructure |(*(*(a, *b)))| forces AssignDestructure to allocate a rest array at
// three depths. It proves the accounting is recursive over arbitrary nesting rather
// than patched per shape: a quota that fits the outer and middle rest arrays but not
// the innermost must trip, and a quota that fits all three must admit the walk.
func TestHashEachDeepRestTargetDestructureReservesEveryDepth(t *testing.T) {
	t.Parallel()

	const count = 50_000
	receiver := largeHashReceiver(count)
	block := deepRestTargetDestructureBlock()
	if valueBlock(block).Params[0].Target == nil {
		t.Fatal("test setup expects a |(*(*(a, *b)))| destructuring parameter (non-nil Target)")
	}
	if !blockCanReuseEnv(valueBlock(block)) {
		t.Fatal("test setup expects the deep rest-target block to reuse its environment")
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	// The outer rest collects the two-element pair, the middle rest collects that
	// two-element array, and the innermost rest collects the one element after a.
	outerRestBytes := restArrayBytes(collapsedPairElements)
	middleRestBytes := restArrayBytes(collapsedPairElements)
	innerRestBytes := restArrayBytes(collapsedPairElements - 1)

	// A quota fitting roots, scratch, two pairs, and only the outer and middle rest
	// arrays -- but not the innermost -- must reject once the deepest rest is charged.
	withoutInnerQuota := roots + scratch + 2*collapsedPairBytes + outerRestBytes + middleRestBytes
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: withoutInnerQuota}
	if _, err := callHashMember(t, exec, receiver, "each", nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("deep rest-target each missing the innermost rest (quota %d) = %v, want errMemoryQuotaExceeded", withoutInnerQuota, err)
	}

	// Room for all three nested rest arrays admits the walk.
	allRestQuota := roots + scratch + 2*collapsedPairBytes + outerRestBytes + middleRestBytes + innerRestBytes
	exec = &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: allRestQuota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("deep rest-target each at a quota fitting all three rest arrays %d = %v, want success", allRestQuota, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != count {
		t.Fatalf("deep rest-target each returned %v with %d entries, want the %d-entry receiver", got.Kind(), len(got.Hash()), count)
	}
}

// mutatingNestedRestEachBlock builds a |(k, (head, *tail))| block whose body grows
// a not-yet-visited entry of the receiver to a large array on the matching key. The
// receiver is captured in the block's environment under receiverName so the body's
// IndexExpr assignment mutates it in place, exactly as a script's
// `data[growKey] = [...]` would. It backs the regression for the P2 finding on PR
// #808: Hash#each's preflight reservation sizes the peak rest array from the
// entries' values before the walk, so this block lets a value grow past that
// reservation mid-walk and the iterator's per-entry projection must reject the
// grown entry before AssignDestructure materializes its over-quota rest array.
func mutatingNestedRestEachBlock(receiver Value, receiverName, growKey string, grownLen int) Value {
	pos := Position{Line: 1, Column: 1}
	target := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "k", Position: pos}},
			{Target: &DestructureTarget{
				Position: pos,
				Elements: []DestructureElement{
					{Target: &Identifier{Name: "head", Position: pos}},
					{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
				},
			}},
		},
	}
	elems := make([]Expression, grownLen)
	for i := range elems {
		elems[i] = &IntegerLiteral{Value: int64(i), Position: pos}
	}
	body := []Statement{
		&AssignStmt{
			Target: &IndexExpr{
				Object:   &Identifier{Name: receiverName, Position: pos},
				Index:    &SymbolLiteral{Name: growKey, Position: pos},
				Position: pos,
			},
			Value:    &ArrayLiteral{Elements: elems, Position: pos},
			Position: pos,
		},
	}
	env := newEnv(nil)
	env.Define(receiverName, receiver)
	return NewBlock([]Param{{Kind: ParamNormal, Target: target}}, body, env)
}

// twoSmallArrayEntryHash builds the {a: [1, 2], b: [1, 2]} receiver the mutation
// regressions walk. Both values start small so the preflight reserves a tiny rest
// array; the block then grows :b before the sorted walk reaches it.
func twoSmallArrayEntryHash() Value {
	return NewHash(map[string]Value{
		"a": NewArray([]Value{NewInt(1), NewInt(2)}),
		"b": NewArray([]Value{NewInt(1), NewInt(2)}),
	})
}

// TestHashEachDestructureRestRejectsGrownEntryBeforeAllocating pins the P2 finding
// on PR #808 (members_hash.go:346): Hash#each's preflight sizes its peak rest array
// from the entries' values as they stand before the walk, but a destructuring block
// with a rest target can mutate a not-yet-visited entry to a far larger value
// (data[:b] = bigArray while binding :a). The sorted walk reaches :b only after the
// mutation, so binding it makes AssignDestructure collect a tail rest array sized to
// the grown value -- larger than the preflight reserved -- inside callBlock, before
// the body's first in-block memory check runs. The per-entry live projection added
// for this finding reprojects the grown value against the quota and rejects it
// before that allocation. A quota that comfortably fits the body's grown-array
// assignment but not the additional grown rest array must trip the walk.
func TestHashEachDestructureRestRejectsGrownEntryBeforeAllocating(t *testing.T) {
	t.Parallel()

	const grownLen = 4096
	grownArrayBytes := restArrayBytes(grownLen)

	probeReceiver := twoSmallArrayEntryHash()
	probeBlock := mutatingNestedRestEachBlock(probeReceiver, "__receiver__", "b", grownLen)
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(probeReceiver, nil, nil, probeBlock)

	// Room for the (still small) call roots plus the one grown array the body
	// assigns, with generous headroom, so the mutation itself succeeds. It leaves
	// no room for the grown receiver plus the equally large tail rest array binding
	// :b would allocate, so the per-entry projection must reject before that
	// allocation -- without it the rest array materializes and only a later in-body
	// check catches the overflow.
	rejectQuota := roots + grownArrayBytes + 64*1024

	receiver := twoSmallArrayEntryHash()
	block := mutatingNestedRestEachBlock(receiver, "__receiver__", "b", grownLen)
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: rejectQuota}
	if _, err := callHashMember(t, exec, receiver, "each", nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("each over a hash whose block grows :b past the reservation (quota %d) = %v, want errMemoryQuotaExceeded", rejectQuota, err)
	}
}

// TestHashEachDestructureRestAdmitsGrownEntryWithinQuota is the safety twin of the
// rejection regression: when the quota comfortably fits the grown receiver, the two
// live pairs, and the grown tail rest array, the per-entry projection must admit the
// walk. This proves the rejection above comes from the rest array exceeding the
// quota and not from the new projection being categorically over-tight. It also
// pins Ruby's read-the-live-value semantics: binding :b after the mutation must
// destructure the grown value, so tail collects grownLen-1 elements rather than the
// original one.
func TestHashEachDestructureRestAdmitsGrownEntryWithinQuota(t *testing.T) {
	t.Parallel()

	const grownLen = 4096
	grownArrayBytes := restArrayBytes(grownLen)

	probeReceiver := twoSmallArrayEntryHash()
	probeBlock := mutatingNestedRestEachBlock(probeReceiver, "__receiver__", "b", grownLen)
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(probeReceiver, nil, nil, probeBlock)

	// Generous room for the grown receiver, the transient grown array literal the
	// body re-evaluates, the grown tail rest array, and per-iteration binding
	// overhead. The walk must complete and leave :b grown to grownLen elements,
	// proving the block bound the live (mutated) value.
	admitQuota := roots + 4*grownArrayBytes + 512*1024

	receiver := twoSmallArrayEntryHash()
	block := mutatingNestedRestEachBlock(receiver, "__receiver__", "b", grownLen)
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: admitQuota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("each over the same mutating block at a roomy quota %d = %v, want success", admitQuota, err)
	}
	if got.Kind() != KindHash {
		t.Fatalf("each returned %v, want the receiver hash", got.Kind())
	}
	grown := receiver.Hash()["b"]
	if grown.Kind() != KindArray || len(grown.Array()) != grownLen {
		t.Fatalf("entry :b after the walk = %v with %d elements, want a %d-element array (the block must bind and keep the live grown value)", grown.Kind(), len(grown.Array()), grownLen)
	}
}

// threeSmallArrayEntryHash builds {a: [1, 2], b: [1, 2], c: [1, 2]}: three sorted
// keys whose values all start small so the preflight reserves a tiny rest array.
// The walk binds :a first and can grow :c -- visited two iterations later -- before
// the sorted walk reaches it.
func threeSmallArrayEntryHash() Value {
	return NewHash(map[string]Value{
		"a": NewArray([]Value{NewInt(1), NewInt(2)}),
		"b": NewArray([]Value{NewInt(1), NewInt(2)}),
		"c": NewArray([]Value{NewInt(1), NewInt(2)}),
	})
}

// mutatingNestedRestEachBlockOnKey builds the nested-rest |(k, (head, *tail))| block
// but grows growKey only while binding bindKey, so the mutation lands on one specific
// iteration. Pointing bindKey at the first sorted key and growKey at the last makes
// the grown entry visited two iterations after the mutation -- the shape a recheck
// that fires only on the iteration immediately following a mutation would miss.
func mutatingNestedRestEachBlockOnKey(receiver Value, receiverName, bindKey, growKey string, grownLen int) Value {
	pos := Position{Line: 1, Column: 1}
	target := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "k", Position: pos}},
			{Target: &DestructureTarget{
				Position: pos,
				Elements: []DestructureElement{
					{Target: &Identifier{Name: "head", Position: pos}},
					{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
				},
			}},
		},
	}
	elems := make([]Expression, grownLen)
	for i := range elems {
		elems[i] = &IntegerLiteral{Value: int64(i), Position: pos}
	}
	// if k == :<bindKey> then receiver[:<growKey>] = [0, 1, ...] end
	body := []Statement{
		&IfStmt{
			Position: pos,
			Condition: &BinaryExpr{
				Position: pos,
				Operator: tokenEQ,
				Left:     &Identifier{Name: "k", Position: pos},
				Right:    &SymbolLiteral{Name: bindKey, Position: pos},
			},
			Consequent: []Statement{
				&AssignStmt{
					Target: &IndexExpr{
						Object:   &Identifier{Name: receiverName, Position: pos},
						Index:    &SymbolLiteral{Name: growKey, Position: pos},
						Position: pos,
					},
					Value:    &ArrayLiteral{Elements: elems, Position: pos},
					Position: pos,
				},
			},
		},
	}
	env := newEnv(nil)
	env.Define(receiverName, receiver)
	return NewBlock([]Param{{Kind: ParamNormal, Target: target}}, body, env)
}

// threeEntryHashWithGrownKey builds {a: [1, 2], b: [1, 2], c: <grownLen-element>}:
// the post-mutation receiver shape so a probe can measure the live call roots the
// walk reprojects against once :c has grown. The values alias fresh arrays so the
// estimator sizes the grown :c the same way the live walk does.
func threeEntryHashWithGrownKey(grownKey string, grownLen int) Value {
	grown := make([]Value, grownLen)
	for i := range grown {
		grown[i] = NewInt(int64(i))
	}
	entries := map[string]Value{
		"a": NewArray([]Value{NewInt(1), NewInt(2)}),
		"b": NewArray([]Value{NewInt(1), NewInt(2)}),
		"c": NewArray([]Value{NewInt(1), NewInt(2)}),
	}
	entries[grownKey] = NewArray(grown)
	return NewHash(entries)
}

// TestHashEachDestructureRestRejectsLaterGrownEntryBeforeAllocating pins the part of
// the P2 finding that a mutation-epoch-only recheck would miss: a destructuring block
// with a rest target grows a not-yet-visited entry the walk reaches two iterations
// later (h[:c] = bigArray while binding :a, over sorted keys a < b < c). A recheck
// that fired only on the single iteration right after a mutation would reproject :b
// (still small) and then bind the grown :c with no recheck, letting AssignDestructure
// collect an over-quota tail rest array inside callBlock. Because every iteration
// sizes its own live rest against the reservation, the walk rejects when it reaches
// :c no matter how many small entries sit between the mutation and the grown entry.
//
// The quota is set in the narrow window that isolates the per-iteration rest recheck
// from the epoch's baseline recheck: it admits the grown receiver and the two live
// pairs (so the post-mutation baseline recheck at :b passes), but leaves less than the
// grown tail rest array binding :c would allocate on top. Only the per-iteration rest
// recheck -- which adds that entry's live rest to the baseline -- can reject here; the
// baseline-only recheck and the preflight both pass.
func TestHashEachDestructureRestRejectsLaterGrownEntryBeforeAllocating(t *testing.T) {
	t.Parallel()

	const grownLen = 16384
	// Binding :c destructures its grown value as (head, *tail), so tail collects all
	// but the first element into a fresh rest array.
	tailRestBytes := restArrayBytes(grownLen - 1)

	// Probe the live call roots against the post-mutation receiver (with :c grown), so
	// the quota is sized to the baseline the walk reprojects against at :b -- not the
	// pre-walk snapshot, which would conflate the rest array with the grow itself.
	probeReceiver := threeEntryHashWithGrownKey("c", grownLen)
	probeBlock := mutatingNestedRestEachBlockOnKey(probeReceiver, "__receiver__", "a", "c", grownLen)
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	grownRoots := probe.hashCallRootBytes(probeReceiver, nil, nil, probeBlock)
	scratch := sortedKeyBufferBytes(len(probeReceiver.Hash()))

	// Admit the grown baseline, the sorted-key scratch, and the two live pairs (so the
	// baseline-only recheck at :b passes), plus half the tail rest array -- not the
	// whole of it. The per-iteration rest recheck at :c adds the full tail rest on top
	// of the baseline and must reject; without it AssignDestructure would materialize
	// the over-quota rest array before any later check observed it.
	rejectQuota := grownRoots + scratch + 2*collapsedPairBytes + tailRestBytes/2

	receiver := threeSmallArrayEntryHash()
	block := mutatingNestedRestEachBlockOnKey(receiver, "__receiver__", "a", "c", grownLen)
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: rejectQuota}
	if _, err := callHashMember(t, exec, receiver, "each", nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("each over a hash whose block grows the last-visited :c past the reservation (quota %d) = %v, want errMemoryQuotaExceeded", rejectQuota, err)
	}
}

// TestCheckLiveCollapsedRestBytes pins the per-entry projection's contract in
// isolation: it charges the call roots, the two live collapsed pairs, and the
// supplied live rest footprint, rejecting only when their sum exceeds the quota and
// no-opping when no quota is enforced. Hash#each calls it for any entry whose live
// rest has grown past the preflight reservation (see members_hash.go), so this is
// the guard the P2 finding required.
func TestCheckLiveCollapsedRestBytes(t *testing.T) {
	t.Parallel()

	receiver := twoSmallArrayEntryHash()
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(receiver, nil, nil, NewNil())

	const liveRestBytes = 1 << 20
	atLimit := saturatingAdd(roots, 2*collapsedPairBytes+liveRestBytes)

	// No quota: the projection never rejects, however large the live rest.
	noQuota := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	if err := noQuota.checkLiveCollapsedRestBytes(liveRestBytes, receiver, nil, nil, NewNil()); err != nil {
		t.Fatalf("checkLiveCollapsedRestBytes with no memory quota = %v, want nil", err)
	}

	// A quota exactly equal to the projected peak admits it; one byte less rejects.
	exact := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: atLimit}
	if err := exact.checkLiveCollapsedRestBytes(liveRestBytes, receiver, nil, nil, NewNil()); err != nil {
		t.Fatalf("checkLiveCollapsedRestBytes at the exact projected peak %d = %v, want nil", atLimit, err)
	}
	tight := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: atLimit - 1}
	if err := tight.checkLiveCollapsedRestBytes(liveRestBytes, receiver, nil, nil, NewNil()); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("checkLiveCollapsedRestBytes one byte below the projected peak %d = %v, want errMemoryQuotaExceeded", atLimit-1, err)
	}
}

// TestDestructureTargetHasRest pins the gate Hash#each uses to skip the per-entry
// live projection for destructures that never allocate a value-sized rest. Only a
// target with a rest element at some depth grows its rest array with the value, so
// fixed-arity shapes like |(k, v)| must report false while any rest-bearing shape
// reports true.
func TestDestructureTargetHasRest(t *testing.T) {
	t.Parallel()

	pos := Position{Line: 1, Column: 1}
	fixed := &DestructureTarget{Position: pos, Elements: []DestructureElement{
		{Target: &Identifier{Name: "k", Position: pos}},
		{Target: &Identifier{Name: "v", Position: pos}},
	}}
	if destructureTargetHasRest(fixed) {
		t.Fatal("destructureTargetHasRest(|(k, v)|) = true, want false (no rest element)")
	}

	topLevelRest := &DestructureTarget{Position: pos, Elements: []DestructureElement{
		{Target: &Identifier{Name: "k", Position: pos}},
		{Target: &Identifier{Name: "rest", Position: pos}, Rest: true},
	}}
	if !destructureTargetHasRest(topLevelRest) {
		t.Fatal("destructureTargetHasRest(|(k, *rest)|) = false, want true")
	}

	nestedRest := valueBlock(nestedRestDestructureBlock()).Params[0].Target.(*DestructureTarget)
	if !destructureTargetHasRest(nestedRest) {
		t.Fatal("destructureTargetHasRest(|(k, (head, *tail))|) = false, want true (the rest is nested)")
	}
}

// TestHashEachTwoParamReservesNoPair pins case 0 of the collapsed-pair model: a
// two-parameter block |k, v| auto-splats into key and value separately, so the
// iterator allocates no [key, value] pair array and must reserve zero per-entry
// bytes. A quota sized to exactly the call roots plus the sorted-key scratch must
// admit the walk over a multi-entry hash; charging even one collapsedPairBytes here
// would wrongly reject the auto-splat form.
func TestHashEachTwoParamReservesNoPair(t *testing.T) {
	t.Parallel()

	const count = 50_000
	receiver := largeHashReceiver(count)
	block := twoParamPairBlock()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	scratch := sortedKeyBufferBytes(count)
	if scratch <= 0 {
		t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
	}

	// Exactly the roots and the scratch: no pair reservation. The two-parameter form
	// allocates no pair, so this quota must fit; a quota that also charged one pair
	// would push the projection over and reject.
	noPairQuota := roots + scratch
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: noPairQuota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("two-parameter each at the roots-plus-scratch quota %d = %v, want success", noPairQuota, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != count {
		t.Fatalf("two-parameter each returned %v with %d entries, want the %d-entry receiver", got.Kind(), len(got.Hash()), count)
	}

	// Guard: one byte below the roots-plus-scratch baseline still rejects, proving the
	// success above comes from that baseline fitting exactly and not from slack.
	tight := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: noPairQuota - 1}
	if _, err := callHashMember(t, tight, receiver, "each", nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("two-parameter each one byte below roots+scratch (quota %d) = %v, want errMemoryQuotaExceeded", noPairQuota-1, err)
	}
}

// TestHashEachKeyValueReserveNoPair pins case 0 for each_key and each_value: both
// bind the key or value directly and never materialize a [key, value] pair, so they
// must reserve zero per-entry bytes even when handed a single-parameter block. A
// quota sized to exactly the call roots plus the sorted-key scratch must admit each
// walk over a multi-entry hash.
func TestHashEachKeyValueReserveNoPair(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"each_key", "each_value"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			const count = 50_000
			receiver := largeHashReceiver(count)
			block := singleParamKeyBlock()

			probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
			roots := probe.hashCallRootBytes(receiver, nil, nil, block)
			scratch := sortedKeyBufferBytes(count)
			if scratch <= 0 {
				t.Fatalf("test setup expects a heap-allocated key buffer for %d entries", count)
			}

			// Exactly the roots and the scratch: no pair reservation. These walks bind the
			// key or value directly, so this quota must fit; charging one pair would reject.
			noPairQuota := roots + scratch
			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: noPairQuota}
			got, err := callHashMember(t, exec, receiver, name, nil, block)
			if err != nil {
				t.Fatalf("%s at the roots-plus-scratch quota %d = %v, want success", name, noPairQuota, err)
			}
			if got.Kind() != KindHash || len(got.Hash()) != count {
				t.Fatalf("%s returned %v with %d entries, want the %d-entry receiver", name, got.Kind(), len(got.Hash()), count)
			}

			// Guard: one byte below the roots-plus-scratch baseline still rejects, proving
			// the success comes from that baseline fitting exactly and not from slack.
			tight := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: noPairQuota - 1}
			if _, err := callHashMember(t, tight, receiver, name, nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
				t.Fatalf("%s one byte below roots+scratch (quota %d) = %v, want errMemoryQuotaExceeded", name, noPairQuota-1, err)
			}
		})
	}
}

// TestCollapsedPairReservation pins the per-shape peak collapsedPairReservation
// sizes for Hash#each. The reservation is the contract the iterator's preflight
// honors: over two or more entries the loop reuses one pairArg slot, so the next
// iteration's NewArray is built while pairArg still references the previous pair,
// leaving two pair arrays briefly live. That overlap is independent of how the
// block binds the pair, so both a whole-pair |pair| binding and a destructuring
// |(k, v)| parameter reserve two pairs; a single entry has no previous iteration
// to overlap and reserves one, and an empty receiver reserves none.
func TestCollapsedPairReservation(t *testing.T) {
	t.Parallel()

	multiEntry := largeHashReceiver(3)
	singleEntry := largeHashReceiver(1)
	emptyHash := NewHash(map[string]Value{})
	arrayValues := largeArrayValueHashReceiver(3, 8)

	runnerFor := func(t *testing.T, block Value) *blockCallRunner {
		t.Helper()
		exec := &Execution{ctx: context.Background(), quota: 1 << 30}
		runner, err := newBlockCallRunner(exec, block, "hash.each")
		if err != nil {
			t.Fatalf("newBlockCallRunner = %v", err)
		}
		return runner
	}

	t.Run("whole-pair binding over multiple entries reserves two pairs", func(t *testing.T) {
		t.Parallel()
		runner := runnerFor(t, singleParamPairBlock())
		got := collapsedPairReservation(runner, true, multiEntry.Hash())
		if want := 2 * collapsedPairBytes; got != want {
			t.Fatalf("collapsedPairReservation(|pair|, 3 entries) = %d, want %d (two live pairs)", got, want)
		}
	})

	t.Run("whole-pair binding over one entry reserves one pair", func(t *testing.T) {
		t.Parallel()
		runner := runnerFor(t, singleParamPairBlock())
		got := collapsedPairReservation(runner, true, singleEntry.Hash())
		if want := collapsedPairBytes; got != want {
			t.Fatalf("collapsedPairReservation(|pair|, 1 entry) = %d, want %d (no previous pair to overlap)", got, want)
		}
	})

	t.Run("destructuring parameter over multiple entries also reserves two pairs", func(t *testing.T) {
		t.Parallel()
		runner := runnerFor(t, destructurePairBlock())
		got := collapsedPairReservation(runner, true, multiEntry.Hash())
		if want := 2 * collapsedPairBytes; got != want {
			t.Fatalf("collapsedPairReservation(|(k, v)|, 3 entries) = %d, want %d (the reused pairArg slot still overlaps two pairs)", got, want)
		}
	})

	t.Run("top-level rest reserves two pairs and one rest array", func(t *testing.T) {
		t.Parallel()
		runner := runnerFor(t, destructureRestPairBlock())
		got := collapsedPairReservation(runner, true, multiEntry.Hash())
		if want := 2*collapsedPairBytes + restArrayBytes(collapsedPairElements-1); got != want {
			t.Fatalf("collapsedPairReservation(|(k, *rest)|, 3 entries) = %d, want %d (two pairs plus one rest array)", got, want)
		}
	})

	t.Run("nested rest sizes the rest array from the value length", func(t *testing.T) {
		t.Parallel()
		runner := runnerFor(t, nestedRestDestructureBlock())
		got := collapsedPairReservation(runner, true, arrayValues.Hash())
		// Each value holds 8 elements; the nested rest collects all but head.
		if want := 2*collapsedPairBytes + restArrayBytes(8-1); got != want {
			t.Fatalf("collapsedPairReservation(|(k, (head, *tail))|, 8-element values) = %d, want %d", got, want)
		}
	})

	t.Run("rest target that is a destructure charges its nested rest array", func(t *testing.T) {
		t.Parallel()
		runner := runnerFor(t, restTargetDestructureBlock())
		got := collapsedPairReservation(runner, true, multiEntry.Hash())
		// |(*(head, *tail))|: the outer rest collects the two-element pair, then its
		// destructure rest target recurses and allocates a second rest array holding
		// the one element after head. Both must be charged, not just the outer rest.
		if want := 2*collapsedPairBytes + restArrayBytes(collapsedPairElements) + restArrayBytes(collapsedPairElements-1); got != want {
			t.Fatalf("collapsedPairReservation(|(*(head, *tail))|, 3 entries) = %d, want %d (two pairs plus the outer and nested rest arrays)", got, want)
		}
	})

	t.Run("three-deep rest targets charge a rest array at every depth", func(t *testing.T) {
		t.Parallel()
		runner := runnerFor(t, deepRestTargetDestructureBlock())
		got := collapsedPairReservation(runner, true, multiEntry.Hash())
		// |(*(*(a, *b)))|: the outer rest collects the pair, the middle rest collects
		// that two-element array, and the innermost rest collects the one element
		// after a. The accounting must sum a rest array at all three depths.
		if want := 2*collapsedPairBytes + restArrayBytes(collapsedPairElements) + restArrayBytes(collapsedPairElements) + restArrayBytes(collapsedPairElements-1); got != want {
			t.Fatalf("collapsedPairReservation(|(*(*(a, *b)))|, 3 entries) = %d, want %d (two pairs plus three nested rest arrays)", got, want)
		}
	})

	t.Run("two-parameter block reserves nothing", func(t *testing.T) {
		t.Parallel()
		runner := runnerFor(t, twoParamPairBlock())
		if got := collapsedPairReservation(runner, false, multiEntry.Hash()); got != 0 {
			t.Fatalf("collapsedPairReservation(|k, v|) = %d, want 0 (no collapsed pair)", got)
		}
	})

	t.Run("empty receiver reserves nothing", func(t *testing.T) {
		t.Parallel()
		runner := runnerFor(t, singleParamPairBlock())
		if got := collapsedPairReservation(runner, true, emptyHash.Hash()); got != 0 {
			t.Fatalf("collapsedPairReservation(|pair|, empty) = %d, want 0 (no pair allocated)", got)
		}
	})
}

// overFixedNestedRestPairTarget builds the destructure target |(a, b, c, *(x))|:
// three fixed elements followed by a rest target that is itself a destructure. Over
// a collapsed two-element [key, value] pair the rest index (3) exceeds the value
// count (2), the shape that made destructureRestAllocBytes slice values[restIndex:]
// out of range and panic the host before the fix clamped the low bound.
func overFixedNestedRestPairTarget() *DestructureTarget {
	pos := Position{Line: 1, Column: 1}
	return &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "a", Position: pos}},
			{Target: &Identifier{Name: "b", Position: pos}},
			{Target: &Identifier{Name: "c", Position: pos}},
			{Target: &DestructureTarget{
				Position: pos,
				Elements: []DestructureElement{
					{Target: &Identifier{Name: "x", Position: pos}},
				},
			}, Rest: true},
		},
	}
}

// TestDestructureRestAllocBytesClampsOverFixedRest pins the P1 host-crash finding on
// PR #808: when a destructure has more fixed targets than the value provides plus a
// nested rest target (|(a, b, c, *(x))| over a two-element pair), restIndex exceeds
// the value count. The preflight helper reconstructs the rest slice as
// values[restStart:restEnd]; without clamping the low bound to len(values) -- exactly
// as AssignDestructure does -- that slice panics with a slice-bounds error before any
// binding, a sandbox DoS. The helper must instead return a finite, non-negative byte
// count: the missing fixed targets bind to nil and the rest collects nothing.
func TestDestructureRestAllocBytesClampsOverFixedRest(t *testing.T) {
	t.Parallel()

	target := overFixedNestedRestPairTarget()
	pair := NewArray([]Value{NewSymbol("k"), NewInt(1)})

	got := destructureRestAllocBytes(target, pair)
	if got < 0 {
		t.Fatalf("destructureRestAllocBytes(|(a, b, c, *(x))|, 2-value pair) = %d, want a non-negative count", got)
	}
	// The rest spans no values (restStart is clamped to the value count and restEnd
	// floors at restStart), and its nested target collects nothing, so the only
	// charge is the empty outer rest array.
	if want := restArrayBytes(0); got != want {
		t.Fatalf("destructureRestAllocBytes(|(a, b, c, *(x))|, 2-value pair) = %d, want %d (one empty rest array)", got, want)
	}
}

// overFixedNestedRestPairBlock builds an |(a, b, c, *(x))| block with an EMPTY body.
// wantsCollapsedPair stays true (one positional parameter), so Hash#each collapses
// each entry into a [key, value] pair and binds it through this over-fixed nested
// rest shape -- the shape whose preflight rest reconstruction panicked the host.
func overFixedNestedRestPairBlock() Value {
	params := []Param{{Kind: ParamNormal, Target: overFixedNestedRestPairTarget()}}
	return NewBlock(params, nil, newEnv(nil))
}

// TestHashEachOverFixedNestedRestDoesNotPanic is the end-to-end twin of the P1
// regression: iterating a hash with an |(a, b, c, *(x))| block under a memory quota
// must complete without panicking the host. Each entry's collapsed pair holds only
// two values, fewer than the three fixed targets plus the nested rest, so the
// preflight's rest reconstruction slices out of range unless its low bound is
// clamped. The walk binds the missing fixed targets to nil and an empty rest, exactly
// as AssignDestructure does, and returns the receiver.
func TestHashEachOverFixedNestedRestDoesNotPanic(t *testing.T) {
	t.Parallel()

	receiver := NewHash(map[string]Value{
		"a": NewInt(1),
		"b": NewInt(2),
		"c": NewInt(3),
	})
	block := overFixedNestedRestPairBlock()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	scratch := sortedKeyBufferBytes(len(receiver.Hash()))

	// A real but generous quota so the memory path -- including the preflight's rest
	// reconstruction -- runs under enforcement rather than the no-quota short circuit.
	quota := roots + scratch + 4*collapsedPairBytes + 64*1024
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("each over |(a, b, c, *(x))| at quota %d = %v, want success without panicking", quota, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != len(receiver.Hash()) {
		t.Fatalf("each returned %v with %d entries, want the %d-entry receiver", got.Kind(), len(got.Hash()), len(receiver.Hash()))
	}
}

// mutatingPairValueEachBlock builds a single-parameter |pair| block (no rest, no
// destructuring) whose body grows a not-yet-visited entry of the receiver in place to
// a large array on growKey. The receiver is captured under receiverName so the body's
// `receiver[growKey] = [...]` mutates it exactly as a script would. The collapsed
// pair this block binds is a fixed two-element array, so before the fix the iterator
// reserved its peak only from the pre-walk snapshot and never rechecked it after a
// mutation grew the receiver baseline -- the non-rest P2 gap this regression pins.
func mutatingPairValueEachBlock(receiver Value, receiverName, growKey string, grownLen int) Value {
	pos := Position{Line: 1, Column: 1}
	elems := make([]Expression, grownLen)
	for i := range elems {
		elems[i] = &IntegerLiteral{Value: int64(i), Position: pos}
	}
	body := []Statement{
		&AssignStmt{
			Target: &IndexExpr{
				Object:   &Identifier{Name: receiverName, Position: pos},
				Index:    &SymbolLiteral{Name: growKey, Position: pos},
				Position: pos,
			},
			Value:    &ArrayLiteral{Elements: elems, Position: pos},
			Position: pos,
		},
	}
	env := newEnv(nil)
	env.Define(receiverName, receiver)
	return NewBlock([]Param{{Name: "pair", Kind: ParamNormal}}, body, env)
}

// threeSmallEntryHash builds {a: 1, b: 1, c: 1}: three sorted keys so a block bound to
// :a can grow a later key (:c) before the sorted walk allocates :c's pair.
func threeSmallEntryHash() Value {
	return NewHash(map[string]Value{
		"a": NewInt(1),
		"b": NewInt(1),
		"c": NewInt(1),
	})
}

// TestHashEachFixedPairRejectsGrownBaselineBeforeAllocating exercises the non-rest P2
// finding on PR #808: a plain |pair| (or |(k, v)|) block allocates a fixed
// two-element pair array whose peak the iterator reserves only from the pre-walk
// snapshot. When an earlier iteration grows a not-yet-visited entry in place
// (receiver[:c] = big_array while binding :a) the receiver baseline grows past that
// snapshot, so a later iteration would allocate its pair on the grown baseline. The
// mutation epoch makes the loop reproject the live pair peak against the grown roots
// after the receiver changes, so the walk rejects a quota that fits the grown array
// the body assigns but not the grown baseline plus the two live pairs. The pair
// transient the epoch recheck pre-empts is bounded (two constant pair arrays) and is
// also backstopped by the next iteration's in-body check; this test pins the walk's
// observable rejection, while TestHashEachReadOnlyWalkNeverReprojects pins that the
// recheck stays off the non-mutating fast path.
func TestHashEachFixedPairRejectsGrownBaselineBeforeAllocating(t *testing.T) {
	t.Parallel()

	const grownLen = 8192
	grownArrayBytes := restArrayBytes(grownLen)

	probeReceiver := threeSmallEntryHash()
	probeBlock := mutatingPairValueEachBlock(probeReceiver, "__receiver__", "c", grownLen)
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(probeReceiver, nil, nil, probeBlock)
	scratch := sortedKeyBufferBytes(len(probeReceiver.Hash()))

	// Room for the small call roots, the scratch, the one grown array the body
	// assigns to :c, and the two live pairs -- but nothing more. The first iteration
	// (:a) assigns the grown array, leaving the live baseline at roughly roots +
	// grownArrayBytes. The next iterations must reproject that grown baseline plus the
	// two live pairs; with no headroom past the two pairs the projection trips,
	// proving the post-mutation recheck guards the fixed-pair allocation.
	rejectQuota := roots + scratch + grownArrayBytes + 2*collapsedPairBytes
	receiver := threeSmallEntryHash()
	block := mutatingPairValueEachBlock(receiver, "__receiver__", "c", grownLen)
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: rejectQuota}
	if _, err := callHashMember(t, exec, receiver, "each", nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
		t.Fatalf("fixed-pair each whose block grows :c past the snapshot (quota %d) = %v, want errMemoryQuotaExceeded", rejectQuota, err)
	}
}

// TestHashEachFixedPairAdmitsGrownBaselineWithinQuota is the safety twin of the
// rejection regression: when the quota comfortably fits the grown receiver and the two
// live pairs, the post-mutation reprojection must admit the walk rather than rejecting
// categorically. This proves the rejection above comes from the quota being tight and
// not from the recheck being over-eager, and that a read-the-live-value walk over a
// fixed |pair| block completes after an in-place mutation.
func TestHashEachFixedPairAdmitsGrownBaselineWithinQuota(t *testing.T) {
	t.Parallel()

	const grownLen = 8192
	grownArrayBytes := restArrayBytes(grownLen)

	probeReceiver := threeSmallEntryHash()
	probeBlock := mutatingPairValueEachBlock(probeReceiver, "__receiver__", "c", grownLen)
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(probeReceiver, nil, nil, probeBlock)
	scratch := sortedKeyBufferBytes(len(probeReceiver.Hash()))

	admitQuota := roots + scratch + 4*grownArrayBytes + 512*1024
	receiver := threeSmallEntryHash()
	block := mutatingPairValueEachBlock(receiver, "__receiver__", "c", grownLen)
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: admitQuota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("fixed-pair each over the mutating block at a roomy quota %d = %v, want success", admitQuota, err)
	}
	if got.Kind() != KindHash {
		t.Fatalf("each returned %v, want the receiver hash", got.Kind())
	}
	grown := receiver.Hash()["c"]
	if grown.Kind() != KindArray || len(grown.Array()) != grownLen {
		t.Fatalf("entry :c after the walk = %v with %d elements, want a %d-element array (the block must keep the live grown value)", grown.Kind(), len(grown.Array()), grownLen)
	}
}

// TestHashEachReadOnlyWalkNeverReprojects pins the fast-path invariant the mutation
// epoch protects: a read-only collapsed-pair walk advances no mutation epoch, so the
// loop never triggers the O(receiver) reprojection. A quota sized to exactly the call
// roots, the sorted-key scratch, and the two live pairs (the preflight reservation)
// must admit a large read-only walk; were the loop reprojecting every entry on top of
// the bound pair, the projection would charge more than the two reserved pairs and
// reject this exact-fit quota.
func TestHashEachReadOnlyWalkNeverReprojects(t *testing.T) {
	t.Parallel()

	const count = 50_000
	receiver := largeHashReceiver(count)
	block := singleParamPairBlock()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	beforeEpoch := probe.mutationEpoch
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	scratch := sortedKeyBufferBytes(count)

	// The two reserved pairs plus a small slack for the interpreter's per-step checks.
	// No reprojection fires, so no extra pair is charged beyond the reservation.
	quota := roots + scratch + 2*collapsedPairBytes + 64*1024
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	if _, err := callHashMember(t, exec, receiver, "each", nil, block); err != nil {
		t.Fatalf("read-only each at the two-pair quota %d = %v, want success (no reprojection on a non-mutating walk)", quota, err)
	}
	if exec.mutationEpoch != beforeEpoch {
		t.Fatalf("read-only each advanced the mutation epoch from %d to %d, want it unchanged", beforeEpoch, exec.mutationEpoch)
	}
}

// TestNoteMutationCoversEveryInPlaceAssignment pins the contract the mutation epoch
// relies on: every assignment that writes into a live container advances the epoch,
// while binding a plain local does not. Hash#each's post-mutation reprojection only
// fires when the epoch changes, so a write that grew a not-yet-visited entry without
// advancing the epoch would silently escape the recheck. The in-place kinds are index
// (arr[i], h[k]), member (obj.prop), instance variable (@x), and class variable
// (@@x); a bare local assignment rebinds the variable and grows no existing container.
func TestNoteMutationCoversEveryInPlaceAssignment(t *testing.T) {
	t.Parallel()

	pos := Position{Line: 1, Column: 1}
	class := &ClassDef{Name: "C", ClassVars: map[string]Value{}}
	self := NewInstance(&Instance{Class: class, Ivars: map[string]Value{}})

	newEnvWithSelf := func() *Env {
		env := newEnv(nil)
		env.Define("self", self)
		env.Define("arr", NewArray([]Value{NewInt(0)}))
		env.Define("h", NewHash(map[string]Value{"k": NewInt(0)}))
		env.Define("obj", NewHash(map[string]Value{"prop": NewInt(0)}))
		env.Define("local", NewInt(0))
		return env
	}

	cases := []struct {
		name        string
		target      Expression
		wantAdvance bool
	}{
		{
			name:        "array index",
			target:      &IndexExpr{Object: &Identifier{Name: "arr", Position: pos}, Index: &IntegerLiteral{Value: 0, Position: pos}, Position: pos},
			wantAdvance: true,
		},
		{
			name:        "hash key",
			target:      &IndexExpr{Object: &Identifier{Name: "h", Position: pos}, Index: &SymbolLiteral{Name: "k", Position: pos}, Position: pos},
			wantAdvance: true,
		},
		{
			name:        "member property",
			target:      &MemberExpr{Object: &Identifier{Name: "obj", Position: pos}, Property: "prop", Position: pos},
			wantAdvance: true,
		},
		{
			name:        "instance variable",
			target:      &IvarExpr{Name: "x", Position: pos},
			wantAdvance: true,
		},
		{
			name:        "class variable",
			target:      &ClassVarExpr{Name: "y", Position: pos},
			wantAdvance: true,
		},
		{
			name:        "plain local",
			target:      &Identifier{Name: "local", Position: pos},
			wantAdvance: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exec := &Execution{ctx: context.Background(), quota: 1 << 30}
			env := newEnvWithSelf()
			before := exec.mutationEpoch
			if err := exec.assign(tc.target, NewInt(1), env); err != nil {
				t.Fatalf("assign %s = 1: %v", tc.name, err)
			}
			advanced := exec.mutationEpoch != before
			if advanced != tc.wantAdvance {
				t.Fatalf("assign %s advanced the mutation epoch = %t, want %t", tc.name, advanced, tc.wantAdvance)
			}
		})
	}
}
