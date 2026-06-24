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
	if err := probe.add("k0", NewString(strings.Repeat("x", payloadBytes))); err != nil {
		t.Fatalf("probe first entry error = %v", err)
	}
	builtOneEntry := probe.built
	if err := probe.add("k1", NewString(strings.Repeat("y", payloadBytes))); err != nil {
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
	if err := unreserved.add("k0", NewString(strings.Repeat("x", payloadBytes))); err != nil {
		t.Fatalf("first entry tripped the quota without a scratch reservation: %v", err)
	}
	if err := unreserved.add("k1", NewString(strings.Repeat("y", payloadBytes))); err != nil {
		t.Fatalf("test setup expects two entries to fit without the scratch reservation: %v", err)
	}

	// Reserving the scratch consumes the headroom the second entry relied on, so the
	// first entry fits but the second is rejected.
	reserved := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	if err := reserved.reserveScratch(scratchBytes); err != nil {
		t.Fatalf("reserving the scratch tripped the quota before any entry: %v", err)
	}
	if err := reserved.add("k0", NewString(strings.Repeat("x", payloadBytes))); err != nil {
		t.Fatalf("first entry tripped the quota with the scratch reserved, want it to fit: %v", err)
	}
	err := reserved.add("k1", NewString(strings.Repeat("y", payloadBytes)))
	requireErrorIs(t, err, errMemoryQuotaExceeded)
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
	for _, key := range sortedHashKeysInto(receiver.Hash(), nil) {
		if err := acc.add(key, NewNil()); err != nil {
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

// mergeCollidingCyclicBlockSource builds a merge that folds collisions one-key
// hashes all colliding on :x, whose conflict block returns a fresh cyclic value
// per collision: a two-element array carrying a payloadBytes string at index 0 and
// a self-edge at index 1 (c[1] = c). Each result overwrites the same slot, so the
// final map holds one entry, but every intermediate cyclic value is built and then
// replaced.
func mergeCollidingCyclicBlockSource(collisions, payloadBytes int) string {
	var b strings.Builder
	b.WriteString("def run(a)\n  a.merge(")
	for i := range collisions {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("{ x: 1 }")
	}
	fmt.Fprintf(&b, ") { |k, old, new| c = [\"\".ljust(%d, \"z\"), 0]; c[1] = c; c }\nend", payloadBytes)
	return b.String()
}

// TestHashMergeCollidingCyclicBlockReplacementsSucceed is the end-to-end twin of
// TestHashBuildAccumulatorReleasesCyclicValuesFully: a merge conflict block returns
// a fresh self-cyclic value per collision (c = [big, 0]; c[1] = c). Each overwrites
// the same slot, so the final map holds a single cyclic entry whose footprint fits
// comfortably under the quota, and the merge must succeed.
//
// Before the payload walk was made cycle-safe, releasing a replaced cyclic value
// early-returned on its self-edge and never credited its bytes (and the big string
// nested under it) back, so the accumulator's running total climbed by a full
// payload per collision and falsely tripped the quota partway through -- the exact
// false-rejection class this PR removes. The cycle-safe walk releases each replaced
// cycle fully, keeping the running total at one entry.
func TestHashMergeCollidingCyclicBlockReplacementsSucceed(t *testing.T) {
	t.Parallel()

	const collisions = 60
	const payloadBytes = 4 * 1024
	receiver := NewHash(map[string]Value{"x": NewInt(0)})

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(receiver, nil, nil, NewNil())
	// Room for the receiver, the script's AST and environment, and one final cyclic
	// entry holding a single payload, but far less than the sum of every collision's
	// payload. The pre-fix leak charged one payload per collision and tripped here.
	quota := liveWithRoots + payloadBytes + 60*1024

	// Sanity: the pre-fix per-collision leak (a full payload plus the leaked cyclic
	// backing per collision) dwarfs this quota, so success proves the cycle is
	// released rather than the quota being slack enough to hide the leak.
	leaked := collisions * payloadBytes
	if leaked <= quota {
		t.Fatalf("test setup expects the per-collision leak (%d) to exceed the quota (%d)", leaked, quota)
	}

	source := mergeCollidingCyclicBlockSource(collisions, payloadBytes)
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: quota}, source)
	got, err := script.Call(context.Background(), "run", []Value{receiver}, CallOptions{})
	if err != nil {
		t.Fatalf("merge of %d colliding cyclic-block collisions under a fitting quota = %v, want success", collisions, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != 1 {
		t.Fatalf("merge produced %v with %d entries, want a hash with 1 entry", got.Kind(), len(got.Hash()))
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

// TestHashBuildAccumulatorReplacementKeepsReachablePayload pins the P1 finding on
// PR #776 at the accumulator level: when a slot is overwritten with the value it
// already holds (a merge block returning the `old` value), the live footprint is
// unchanged, so built must not drop. The pre-fix net-swap delta trusted the
// persistent estimator's stateful dedup, which deduplicated the returned-old value
// to ~0 and then subtracted the slot's full recorded bytes, dropping built below
// the still-reachable payload and letting a later insert materialize past the
// quota. Reference counting keeps the shared payload charged.
//
// The accumulator is driven directly with controlled Values so the trajectory is
// deterministic: a fresh large string is stored, returned as old, then a second
// fresh large string is stored under a different key. With the fix both payloads
// stay charged and the second insert trips the quota; the pre-fix code dropped the
// first payload on the returns-old write and admitted the second insert.
func TestHashBuildAccumulatorReplacementKeepsReachablePayload(t *testing.T) {
	t.Parallel()

	const payload = 256 * 1024
	bigX := NewString(strings.Repeat("x", payload))
	bigZ := NewString(strings.Repeat("z", payload))

	// Build the accumulator with no memory pressure first to learn its baseline, so
	// the quota can be placed in the window between the buggy single-payload peak and
	// the correct two-payload peak independent of estimator constants.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1}
	probeAcc := newHashBuildAccumulator(probe, NewNil(), nil, nil, NewNil())
	base := probeAcc.base

	// Window: above base + one payload (buggy peak) and below base + two payloads
	// (correct peak), with a half-payload margin so neither boundary is grazed.
	quota := base + payload + payload/2

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	acc := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())

	mustAdd := func(key string, val Value) {
		t.Helper()
		if err := acc.add(key, val); err != nil {
			t.Fatalf("add(%q): unexpected quota error: %v", key, err)
		}
	}

	// Simulate a merge conflict folding over key x: store a fresh large value, then
	// have the block return that same value (old). The footprint after both writes
	// is one payload; built must reflect it.
	mustAdd("x", NewInt(0))
	mustAdd("x", bigX)
	builtAfterStore := acc.built
	mustAdd("x", bigX) // returns old: same backing, footprint unchanged.
	if acc.built < builtAfterStore {
		t.Fatalf("returns-old write dropped built from %d to %d; the reachable payload was un-charged", builtAfterStore, acc.built)
	}

	// A second fresh large value under a different key pushes the true footprint to
	// two payloads, past the quota. The fix keeps x's payload charged, so this insert
	// is rejected; the pre-fix code would have dropped it and admitted this write.
	err := acc.add("z", bigZ)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
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

// mutualCyclicArrays builds two arrays that reference each other: a = [b], b = [a].
func mutualCyclicArrays() (Value, Value) {
	aBacking := make([]Value, 1)
	bBacking := make([]Value, 1)
	a := NewArray(aBacking)
	b := NewArray(bBacking)
	aBacking[0] = b
	bBacking[0] = a
	return a, b
}

// selfCyclicHash builds a hash that holds a reference to itself under one key:
// obj = {}; obj["self"] = obj, the map analog of selfCyclicArray.
func selfCyclicHash() Value {
	backing := make(map[string]Value, 1)
	h := NewHash(backing)
	backing["self"] = h
	return h
}

// TestHashBuildAccumulatorReleasesCyclicValuesFully pins the cycle-safety of the
// reference-counting payload walk. A transform block can return a value that
// reaches itself through Go-level index assignment (a = [0]; a[0] = a, or
// obj.Hash()[k] = obj). Charging and releasing such a value must be
// mirror-symmetric: the walk visits each backing once per charge and once per
// release, so a key repeatedly overwritten with a cyclic value holds a constant
// live footprint and leaves no orphaned reference-count entries behind.
//
// Without the per-walk visited set the release walk early-returns on the cyclic
// backing -- its self-edge keeps the refcount positive, so release never recurses
// and the backing's bytes are never credited back. built then climbs on every
// replacement (an over-count that causes false rejections) and valueRefs leaks one
// entry per replacement.
func TestHashBuildAccumulatorReleasesCyclicValuesFully(t *testing.T) {
	t.Parallel()

	shapes := []struct {
		name  string
		build func() Value
	}{
		{name: "self_cyclic_array", build: selfCyclicArray},
		{name: "self_cyclic_hash", build: selfCyclicHash},
		{
			name: "mutual_cyclic_arrays",
			build: func() Value {
				a, _ := mutualCyclicArrays()
				return a
			},
		},
		{
			name: "cycle_nested_under_fresh_container",
			build: func() Value {
				// A fresh outer array wrapping a self-cyclic inner array, so the walk
				// must descend through a non-cyclic backing before hitting the cycle.
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
			// Ample headroom: the footprint of a single cyclic value is tiny, and the
			// test asserts built stays constant rather than tripping, so the quota only
			// needs to admit one steady-state entry.
			exec.memoryQuota = base + estimatedValueBytes + estimatedMapBaseBytes + 64*1024

			acc := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())

			// Seed the slot with a first cyclic value, then record the steady-state
			// footprint after one replacement so the comparison ignores the one-time
			// tracking-map and entry costs of the first insert.
			if err := acc.add("k", shape.build()); err != nil {
				t.Fatalf("seeding the cyclic slot tripped the quota: %v", err)
			}
			if err := acc.add("k", shape.build()); err != nil {
				t.Fatalf("first cyclic replacement tripped the quota: %v", err)
			}
			steady := acc.built
			steadyRefs := len(acc.valueRefs)

			// Replace the same slot with a fresh distinct cyclic value many times. Each
			// replacement charges a new cycle and releases the old one; a cycle-safe walk
			// keeps built and the reference-count map at their steady-state size.
			const replacements = 50
			for i := range replacements {
				if err := acc.add("k", shape.build()); err != nil {
					t.Fatalf("cyclic replacement %d tripped the quota: %v", i, err)
				}
				if acc.built != steady {
					t.Fatalf("cyclic replacement %d changed built from %d to %d; the cyclic payload is being charged or released asymmetrically",
						i, steady, acc.built)
				}
				if len(acc.valueRefs) != steadyRefs {
					t.Fatalf("cyclic replacement %d changed valueRefs size from %d to %d; a cyclic backing leaked a reference-count entry",
						i, steadyRefs, len(acc.valueRefs))
				}
			}

			// Draining the slot to a payload-free scalar must release every cyclic
			// backing: with no payload-bearing value left, the reference-count map must
			// be empty. A leaked self-edge would keep an orphaned entry alive here.
			if err := acc.add("k", NewInt(0)); err != nil {
				t.Fatalf("draining the slot tripped the quota: %v", err)
			}
			if len(acc.valueRefs) != 0 {
				t.Fatalf("valueRefs holds %d orphaned entries after draining the cyclic slot, want 0", len(acc.valueRefs))
			}
		})
	}
}

// TestHashBuildAccumulatorChargesValueRefsMapBase pins that the reference-counting
// bookkeeping map (valueRefs) charges its own empty-map base the first time a fresh
// payload backing is referenced, mirroring how add charges the storedValues base on
// its first insert. Without that charge the live hmap is invisible to the quota: it
// is allocated during the build but never accounted, so the peak can exceed the
// sandbox by estimatedMapBaseBytes.
//
// The test differences two consecutive inserts of equal-cost fresh-payload values.
// The first insert pays both bookkeeping bases (storedValues and valueRefs) plus
// the shared per-entry and per-ref costs; the second pays only the shared costs. The
// difference is therefore exactly the two map bases. A regression that drops the
// valueRefs base would make the difference a single base and trip this assertion.
func TestHashBuildAccumulatorChargesValueRefsMapBase(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	exec.root = newEnv(nil)
	base := exec.estimateMemoryUsageBase(newMemoryEstimator())
	exec.memoryQuota = base + 1<<20

	acc := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())

	// Two distinct fresh string values of equal length backed by distinct allocations
	// so each charges an identical per-entry and per-ref cost while introducing its
	// own backing. Equal-length distinct keys keep the key-entry cost identical too.
	if err := acc.add("k1", NewString("alpha-payload-0")); err != nil {
		t.Fatalf("first insert tripped the quota: %v", err)
	}
	firstDelta := acc.built

	prev := acc.built
	if err := acc.add("k2", NewString("alpha-payload-1")); err != nil {
		t.Fatalf("second insert tripped the quota: %v", err)
	}
	secondDelta := acc.built - prev

	// The first insert pays the storedValues base and the valueRefs base on top of
	// the shared per-entry/per-ref costs; the second pays neither base. Their
	// difference is exactly the two bookkeeping-map bases. Pre-fix, the valueRefs base
	// was never charged, so the difference would be a single base.
	gotBaseCharges := firstDelta - secondDelta
	wantBaseCharges := 2 * estimatedMapBaseBytes
	if gotBaseCharges != wantBaseCharges {
		t.Fatalf("first-insert base charges = %d bytes, want %d (storedValues base + valueRefs base); the valueRefs bookkeeping map base is not accounted",
			gotBaseCharges, wantBaseCharges)
	}
}

// The P1 "returns-old" finding is pinned at the accumulator level by
// TestHashBuildAccumulatorReplacementKeepsReachablePayload, which drives acc.add
// directly. It deliberately has no end-to-end merge twin: the returns-old shape
// keeps both the returned-old payload and the later fresh payload in the final
// output map, so the post-call checkMemory safety net always observes the
// over-quota result regardless of whether the accumulator under-counts mid-build.
// An end-to-end merge test would therefore pass whether or not the incremental
// accounting is correct, masking the very regression it claims to guard. The
// merge wiring's use of the replacement-aware accumulator is instead covered by
// TestHashMergeManyCollidingOneKeyHashesWithBlockSucceeds and its oversized twin,
// whose transient (overwritten) peaks the post-call check cannot see.

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

// chargedValuePayload returns the pure payload bytes the accumulator's
// reference-counting walk charges for val -- the walk's total charge minus the
// valueRefs bookkeeping it adds on top (one tracking entry per distinct backing
// plus the bookkeeping map's base once any backing is tracked). It runs on a fresh
// accumulator with no call roots so nothing is pre-seen by the baseline, isolating
// the per-value charge for comparison against the estimator.
func chargedValuePayload(t *testing.T, val Value) int {
	t.Helper()
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	exec.root = newEnv(nil)
	acc := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	charged := acc.chargeValuePayload(val)
	bookkeeping := len(acc.valueRefs) * estimatedRefTrackingEntryBytes
	if len(acc.valueRefs) > 0 {
		bookkeeping += estimatedMapBaseBytes
	}
	return charged - bookkeeping
}

// TestHashBuildAccumulatorPayloadMatchesEstimator pins the parity guarantee for the
// P1 finding on PR #776: the accumulator's reference-counting walk must charge a
// value's payload backings exactly what the memory estimator charges for the same
// value (est.valuePayload). The two were a hand-rolled re-implementation of one
// traversal and had diverged twice -- first on cycle safety, then on the per-entry
// value slots of nested hashes (a transform block returning fresh scalar-keyed
// hashes undercounted by len(values)*estimatedValueBytes). Sharing the structural
// byte computation (sliceStructuralBytes, mapStructuralBytes) keeps them in lock
// step; this test enforces that they cannot drift again across representative
// shapes, including the nested-scalar-hash case the finding called out.
func TestHashBuildAccumulatorPayloadMatchesEstimator(t *testing.T) {
	t.Parallel()

	nestedScalarHash := NewHash(map[string]Value{
		"a": NewInt(1),
		"b": NewInt(2),
		"c": NewInt(3),
	})
	hashOfArrays := NewHash(map[string]Value{
		"xs": NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		"ys": NewArray([]Value{NewString("alpha"), NewString("beta")}),
	})
	arrayOfHashes := NewArray([]Value{
		NewHash(map[string]Value{"k": NewInt(1)}),
		NewHash(map[string]Value{"k": NewInt(2), "j": NewInt(3)}),
	})

	// A value that shares a backing internally: the same nested array appears under
	// two keys, so a correct walk reference-counts it once, matching the estimator's
	// dedup by backing pointer.
	shared := NewArray([]Value{NewString("shared-payload-bytes")})
	sharedBackings := NewHash(map[string]Value{
		"left":  shared,
		"right": shared,
	})

	tests := []struct {
		name string
		val  Value
	}{
		{name: "scalar", val: NewInt(42)},
		{name: "string", val: NewString("hello world")},
		{name: "empty hash", val: NewHash(map[string]Value{})},
		{name: "nested hash of scalars", val: nestedScalarHash},
		{name: "hash of arrays", val: hashOfArrays},
		{name: "array of hashes", val: arrayOfHashes},
		{name: "shared backings", val: sharedBackings},
		{
			name: "deeply nested",
			val: NewHash(map[string]Value{
				"meta": NewHash(map[string]Value{
					"tags":  NewArray([]Value{NewString("a"), NewString("b")}),
					"count": NewInt(7),
				}),
				"rows": arrayOfHashes,
			}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := newMemoryEstimator().valuePayload(tc.val)
			got := chargedValuePayload(t, tc.val)
			if got != want {
				t.Fatalf("accumulator charged %d payload bytes, estimator charges %d (they must be identical)", got, want)
			}
		})
	}
}

// TestHashTransformValuesFreshScalarHashesTripsMemoryQuota guards the P1 value-slot
// undercount on PR #776 through the accumulator transform_values drives: a block
// returning a fresh hash of scalar values per entry. Each result hash's per-entry
// value slots (len(values)*estimatedValueBytes) were charged nowhere before the fix,
// so the accumulator's running budget undercounted the build by
// count*slotsPerResult*slot bytes and admitted it -- letting the block-produced
// hashes accumulate past the quota before any check observed the peak.
//
// The test exercises the accumulator directly (the same newHashBuildAccumulator +
// add per entry that transform_values uses) rather than the full builtin, because
// the builtin's post-call check independently rejects an over-quota materialized
// result and would mask whether the accumulator caught the undercount. The quota is
// derived from the estimator's measurement of the fully materialized result -- the
// single source of truth the corrected accumulator now mirrors (see the parity test
// TestHashBuildAccumulatorPayloadMatchesEstimator) -- set just below that true peak.
// The corrected accumulator converges to the estimator's peak and rejects mid-build;
// the pre-fix accounting converged to peak minus the omitted slots and admitted it.
func TestHashTransformValuesFreshScalarHashesTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// The block maps a value to a fresh three-key scalar hash, exactly the
	// transform_values shape the finding called out. Its keys and values are freshly
	// allocated and never alias the empty baseline, so the accumulator charges the
	// result's full payload with no dedup -- the clean case where the omitted value
	// slots are a pure undercount.
	result := NewHash(map[string]Value{
		"a": NewInt(1),
		"b": NewInt(2),
		"c": NewInt(3),
	})
	const key = "row"

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	exec.root = newEnv(nil)
	acc := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	if err := acc.add(key, result); err != nil {
		t.Fatalf("add tripped under an unbounded quota: %v", err)
	}

	// The accumulator's charge for the entry, minus the bookkeeping it adds on top
	// (the storedValues tracking map: its base plus one entry; the valueRefs tracking
	// map: its base plus one entry per distinct backing), must equal exactly what the
	// estimator charges for the same entry: the output map bucket, key header and
	// payload, the value slot, and the result hash's full payload. The result hash's
	// per-entry value slots are part of est.valuePayload, so a walk that omits them
	// charges slotsPerResult*estimatedValueBytes too little and fails this equality.
	bookkeeping := estimatedMapBaseBytes + estimatedTrackingMapEntryBytes // storedValues
	bookkeeping += estimatedMapBaseBytes + len(acc.valueRefs)*estimatedRefTrackingEntryBytes
	chargedPayload := acc.built - bookkeeping

	entry := estimatedMapEntryBytes + estimatedStringHeaderBytes + len(key) + estimatedValueBytes
	want := entry + newMemoryEstimator().valuePayload(result)
	if chargedPayload != want {
		t.Fatalf("accumulator charged %d entry bytes, estimator charges %d (the result hash's value slots must be counted)", chargedPayload, want)
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
