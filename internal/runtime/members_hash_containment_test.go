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

// estimatedEmptyOutputHashBytes is the structural footprint a hash-producing
// transform's projection reserves for its output before any entries: the output
// Value slot, the empty entry map's base overhead, and the hashData wrapper every
// KindHash allocates around that map. It mirrors the non-call-root portion of
// projectedHashBaseBytes so quota-sizing tests reserve exactly what the
// projection charges.
const estimatedEmptyOutputHashBytes = estimatedValueBytes + estimatedMapBaseBytes + estimatedHashDataBytes

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

func typedSymbolHash(key string, val Value) Value {
	hash := NewHash(make(map[string]Value, 1))
	if err := hashSet(hash, NewSymbol(key), val); err != nil {
		panic(fmt.Sprintf("set typed symbol hash key: %v", err))
	}
	return hash
}

// largeTypedSymbolHash builds a typed (symbol-keyed) hash with count entries so
// merge tests exercise the typed projection path (hashHasTypedEntries) rather
// than the legacy string-keyed map.
func largeTypedSymbolHash(count int) Value {
	hash := NewTypedHash(0)
	for i := range count {
		if err := hashSet(hash, NewSymbol("k"+strconv.Itoa(i)), NewInt(int64(i))); err != nil {
			panic(fmt.Sprintf("set typed symbol hash key: %v", err))
		}
	}
	return hash
}

// typedSymbolHashFrom builds a typed (symbol-keyed) hash from name/value pairs.
func typedSymbolHashFrom(pairs map[string]int) Value {
	hash := NewTypedHash(0)
	for k, v := range pairs {
		if err := hashSet(hash, NewSymbol(k), NewInt(int64(v))); err != nil {
			panic(fmt.Sprintf("set typed symbol hash key: %v", err))
		}
	}
	return hash
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

func TestHashKeysValuesHonorSandboxDuringMaterialization(t *testing.T) {
	t.Parallel()
	receiver := largeHashReceiver(2_000)
	baseExec := &Execution{ctx: context.Background(), quota: 1 << 30}
	base := baseExec.estimateMemoryUsage(receiver)
	quota := base + sortedKeyBufferBytes(len(receiver.Hash())) + estimatedSliceBaseBytes

	for _, name := range []string{"keys", "values"} {
		t.Run(name+"_memory", func(t *testing.T) {
			t.Parallel()
			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
			_, err := callHashMember(t, exec, receiver, name, nil, NewNil())
			requireErrorIs(t, err, errMemoryQuotaExceeded)
		})
		t.Run(name+"_canceled", func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			exec := &Execution{ctx: ctx, quota: 1 << 30, memoryQuota: 1 << 30}
			_, err := callHashMember(t, exec, receiver, name, nil, NewNil())
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("hash.%s canceled context error = %v, want context.Canceled", name, err)
			}
		})
	}
}

func TestHashDeepTransformKeysHonorsSandboxDuringTraversal(t *testing.T) {
	t.Parallel()

	nested := NewInt(1)
	for range 2_000 {
		nested = NewArray([]Value{nested})
	}
	receiver := NewHash(map[string]Value{"root": nested})
	exec := &Execution{ctx: context.Background(), quota: 10, memoryQuota: 64 << 20}
	_, err := callHashMember(t, exec, receiver, "deep_transform_keys", nil, keyIdentityBlock())
	requireErrorIs(t, err, errStepQuotaExceeded)
}

func TestHashDeepTransformKeysReservesOutputBuffers(t *testing.T) {
	t.Parallel()

	receiver := largeHashReceiver(2_000)
	block := keyIdentityBlock()
	baseExec := &Execution{ctx: context.Background(), quota: 1 << 30}
	base := baseExec.estimateMemoryUsage(receiver, block)
	quota := base + hashTransformBufferBytes(len(receiver.Hash()), sortedKeyBufferBytes(len(receiver.Hash())))/2
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "deep_transform_keys", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

func TestHashDeepTransformKeysTypedReceiverDoesNotMaterializeLegacyMap(t *testing.T) {
	t.Parallel()

	key := NewArray([]Value{NewString("account"), NewSymbol("id")})
	receiver := NewTypedHash(0)
	if err := hashSet(receiver, key, NewInt(42)); err != nil {
		t.Fatalf("hashSet(%s) error = %v, want nil", key.Inspect(), err)
	}
	if entries, ok := hashStringMapIfMaterialized(receiver); ok || entries != nil {
		t.Fatalf("typed receiver legacy map before deep_transform_keys = %v, %v; want nil, false", entries, ok)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	got, err := callHashMember(t, exec, receiver, "deep_transform_keys", nil, keyIdentityBlock())
	if err != nil {
		t.Fatalf("hash.deep_transform_keys on typed receiver = %v, want success", err)
	}
	if entries, ok := hashStringMapIfMaterialized(receiver); ok || entries != nil {
		t.Fatalf("typed receiver legacy map after deep_transform_keys = %v, %v; want nil, false", entries, ok)
	}
	if got.Kind() != KindHash || got.HashLen() != 1 {
		t.Fatalf("hash.deep_transform_keys typed result = %s with %d entries, want hash with 1 entry", got.Kind(), got.HashLen())
	}
	if value, ok, err := hashGet(got, key); err != nil || !ok || value.Int() != 42 {
		t.Fatalf("hash.deep_transform_keys typed result lookup = (%v, %v, %v), want (42, true, nil)", value, ok, err)
	}
}

func TestHashDeepTransformKeysRetainedPayloadReservationTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	receiver := NewHash(map[string]Value{"root": NewInt(1)})
	block := keyIdentityBlock()
	exec := &Execution{ctx: context.Background(), quota: 1 << 30}
	retainedPayload := 1024
	base := exec.hashCallRootBytes(receiver, nil, nil, block)

	exec.memoryQuota = base + retainedPayload - 1
	delta, err := reserveDeepTransformRetainedPayload(exec, retainedPayload, receiver, nil, nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if delta != 0 {
		t.Fatalf("failed reservation delta = %d, want 0", delta)
	}
	if exec.reservedScratchBytes != 0 {
		t.Fatalf("failed reservation left %d scratch bytes reserved", exec.reservedScratchBytes)
	}

	exec.memoryQuota = base + retainedPayload
	delta, err = reserveDeepTransformRetainedPayload(exec, retainedPayload, receiver, nil, nil, block)
	if err != nil {
		t.Fatalf("reserveDeepTransformRetainedPayload roomy quota error = %v", err)
	}
	if delta != retainedPayload {
		t.Fatalf("reservation delta = %d, want %d", delta, retainedPayload)
	}
	exec.releaseLoopScratch(delta)
	if exec.reservedScratchBytes != 0 {
		t.Fatalf("released reservation left %d scratch bytes reserved", exec.reservedScratchBytes)
	}
}

func TestHashDeepTransformKeysDoesNotRechargeSharedLeafPayloads(t *testing.T) {
	t.Parallel()

	const payloadBytes = 512 * 1024
	receiver := NewHash(map[string]Value{"leaf": NewString(strings.Repeat("x", payloadBytes))})
	block := keyIdentityBlock()
	probe := &Execution{ctx: context.Background(), quota: 1 << 30}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	outputBytes := hashTransformBufferBytes(len(receiver.Hash()), sortedKeyBufferBytes(len(receiver.Hash())))
	quota := liveWithRoots + outputBytes + len("leaf") + 64*1024

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "deep_transform_keys", nil, block)
	if err != nil {
		t.Fatalf("hash.deep_transform_keys with shared string leaf under deduped quota = %v, want success", err)
	}
	if got.Kind() != KindHash {
		t.Fatalf("hash.deep_transform_keys shared string leaf kind = %v, want hash", got.Kind())
	}
	if got.Hash()["leaf"].String() != receiver.Hash()["leaf"].String() {
		t.Fatalf("hash.deep_transform_keys shared string leaf = %q, want original payload", got.Hash()["leaf"].String())
	}
}

func TestHashDeepTransformKeysDoesNotRechargeSharedArrayLeafPayloads(t *testing.T) {
	t.Parallel()

	const payloadBytes = 512 * 1024
	leaf := NewString(strings.Repeat("x", payloadBytes))
	items := NewArray([]Value{leaf})
	receiver := NewHash(map[string]Value{"items": items})
	block := keyIdentityBlock()
	probe := &Execution{ctx: context.Background(), quota: 1 << 30}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	outputBytes := hashTransformBufferBytes(len(receiver.Hash()), sortedKeyBufferBytes(len(receiver.Hash())))
	quota := liveWithRoots + outputBytes + len("items") + deepTransformArrayBufferBytes(len(items.Array())) + 64*1024

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "deep_transform_keys", nil, block)
	if err != nil {
		t.Fatalf("hash.deep_transform_keys with shared array leaf payload under deduped quota = %v, want success", err)
	}
	if got.Kind() != KindHash {
		t.Fatalf("hash.deep_transform_keys shared array leaf kind = %v, want hash", got.Kind())
	}
	gotItems := got.Hash()["items"]
	if gotItems.Kind() != KindArray || len(gotItems.Array()) != 1 || gotItems.Array()[0].String() != leaf.String() {
		t.Fatalf("hash.deep_transform_keys shared array leaf = %#v, want one original string leaf", gotItems)
	}
}

func TestHashDeepTransformKeysDoesNotRechargePreReservedArrayBacking(t *testing.T) {
	t.Parallel()

	items := NewArray([]Value{NewInt(1)})
	receiver := NewHash(map[string]Value{"items": items})
	block := keyIdentityBlock()
	probe := &Execution{ctx: context.Background(), quota: 1 << 30}
	liveWithRoots := probe.hashCallRootBytes(receiver, nil, nil, block)
	outputBytes := hashTransformBufferBytes(len(receiver.Hash()), sortedKeyBufferBytes(len(receiver.Hash())))
	quota := liveWithRoots + outputBytes + len("items") + deepTransformArrayBufferBytes(len(items.Array()))

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "deep_transform_keys", nil, block)
	if err != nil {
		t.Fatalf("hash.deep_transform_keys at pre-reserved array backing quota = %v, want success", err)
	}
	gotItems := got.Hash()["items"]
	if gotItems.Kind() != KindArray || len(gotItems.Array()) != 1 || gotItems.Array()[0].Int() != 1 {
		t.Fatalf("hash.deep_transform_keys array result = %#v, want [1]", gotItems)
	}
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
	liveWithRoot := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, NewNil())
	outputStructure := estimatedEmptyOutputHashBytes +
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
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, args, nil, NewNil())
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	outputStructure := estimatedEmptyOutputHashBytes + len(receiver.Hash())*perEntry
	quota := liveWithRoots + outputStructure

	// Sanity: a map projected at len(args) would dwarf this quota, so admitting the
	// call proves the projection is bounded by the receiver, not the candidate list.
	argSizedStructure := estimatedEmptyOutputHashBytes + argCount*perEntry
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
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, []Value{receiver}, nil, NewNil())
	outputStructure := estimatedEmptyOutputHashBytes +
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
	sumProjection := estimatedEmptyOutputHashBytes +
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
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, args, nil, NewNil())
	outputStructure := estimatedEmptyOutputHashBytes +
		count*(estimatedMapEntryBytes+estimatedStringHeaderBytes+estimatedValueBytes)
	// The per-argument sorted key scratch buffer is reused across arguments, so it
	// only sizes to the largest single argument (count keys here). Fold it into the
	// quota alongside the union-sized output map.
	scratch := sortedKeyBufferBytes(count)
	quota := liveWithRoots + outputStructure + scratch

	// Sanity: the loose upper bound sums every argument length, so it exceeds the
	// union-sized quota and the exact count path must run for this merge to pass.
	looseProjection := estimatedEmptyOutputHashBytes +
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

func TestTypedMergedKeyCount(t *testing.T) {
	t.Parallel()

	base := typedSymbolHashFrom(map[string]int{"a": 1, "b": 2})

	tests := []struct {
		name  string
		args  []Value
		limit int
		want  int
	}{
		{name: "no args", limit: 100, want: 2},
		{name: "self overlay", args: []Value{base}, limit: 100, want: 2},
		{name: "all new keys", args: []Value{typedSymbolHashFrom(map[string]int{"c": 3, "d": 4})}, limit: 100, want: 4},
		{name: "partial overlap", args: []Value{typedSymbolHashFrom(map[string]int{"b": 9, "c": 3})}, limit: 100, want: 3},
		{
			name: "duplicate new key across args",
			args: []Value{
				typedSymbolHashFrom(map[string]int{"c": 3}),
				typedSymbolHashFrom(map[string]int{"c": 4}),
			},
			limit: 100,
			want:  3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exec := &Execution{ctx: context.Background()}
			got, err := typedMergedKeyCount(exec, base, tc.args, tc.limit)
			if err != nil {
				t.Fatalf("typedMergedKeyCount(%s) error = %v, want nil", tc.name, err)
			}
			if got != tc.want {
				t.Fatalf("typedMergedKeyCount(%s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestTypedMergedKeyCountStopsAtLimit verifies the typed union counter never
// grows its deduplication set past the quota-derived budget, mirroring the
// legacy mergedKeyCount cap.
func TestTypedMergedKeyCountStopsAtLimit(t *testing.T) {
	t.Parallel()

	base := typedSymbolHashFrom(map[string]int{"a": 1})
	first := map[string]int{}
	second := map[string]int{}
	for i := range 500 {
		first["f"+strconv.Itoa(i)] = i
		second["s"+strconv.Itoa(i)] = i
	}
	args := []Value{typedSymbolHashFrom(first), typedSymbolHashFrom(second)}

	const limit = 10
	exec := &Execution{ctx: context.Background()}
	got, err := typedMergedKeyCount(exec, base, args, limit)
	if err != nil {
		t.Fatalf("typedMergedKeyCount error = %v, want nil", err)
	}
	if got <= limit {
		t.Fatalf("typedMergedKeyCount returned %d, want a value greater than the limit %d", got, limit)
	}
	if got > limit+1 {
		t.Fatalf("typedMergedKeyCount returned %d, want it to stop at limit+1 (%d)", got, limit+1)
	}
}

func TestTypedHashMergeProjectionCountsUnionNotSum(t *testing.T) {
	t.Parallel()

	// A typed hash merged with itself collapses to its own keys, so a quota that
	// fits the live roots, a union-sized output map, and the sorted scratch buffer
	// must admit it. The pre-fix typed projection summed receiver+arg lengths and
	// would have rejected this self-overlay even though it stays within the limit.
	const count = 2_000
	receiver := largeTypedSymbolHash(count)
	args := []Value{receiver}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	base := probe.projectedHashBaseBytes(receiver, args, nil, NewNil())
	scratch := sortedHashEntryBufferBytes(count)
	quota := base + count*estimatedMapEntryStructuralBytes + scratch

	// Sanity: the discarded sum-based projection (2*count entries) would not fit
	// this quota, confirming the test exercises the typed union fix.
	if base+2*count*estimatedMapEntryStructuralBytes+scratch <= quota {
		t.Fatalf("test setup expects the sum-based projection to exceed the quota")
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "merge", args, NewNil())
	if err != nil {
		t.Fatalf("typed merge(self) under union-sized quota = %v, want success", err)
	}
	if got.Kind() != KindHash || got.HashLen() != count {
		t.Fatalf("typed merge(self) produced %v with %d entries, want a hash with %d", got.Kind(), got.HashLen(), count)
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
	live := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, args, nil, NewNil())
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	return live + estimatedEmptyOutputHashBytes + entries*perEntry
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
	receiverLive := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, NewNil())
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	quota := receiverLive + estimatedEmptyOutputHashBytes + len(receiver.Hash())*perEntry + 4*1024

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
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, args, nil, NewNil())
	outputStructure := estimatedEmptyOutputHashBytes +
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

func TestHashExceptTypedArrayKeyChargesCanonicalExclusionPayload(t *testing.T) {
	t.Parallel()

	keyItems := make([]Value, 256)
	for i := range keyItems {
		keyItems[i] = NewString("segment-" + strconv.Itoa(i))
	}
	key := NewArray(keyItems)
	receiver := NewHash(map[string]Value{})
	if err := hashSet(receiver, key, NewInt(1)); err != nil {
		t.Fatalf("set typed array key: %v", err)
	}
	args := []Value{key}

	lookupKey, err := hashLookupKey(key)
	if err != nil {
		t.Fatalf("lookup typed array key: %v", err)
	}
	extraPayload := lookupKey.ExtraPayloadBytes()
	if extraPayload <= 0 {
		t.Fatalf("expected array lookup key to retain canonical payload, got %d", extraPayload)
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	projected := probe.projectedHashBaseBytes(receiver, args, nil, NewNil())
	projected = saturatingAdd(projected, estimatedMapEntryStructuralBytes)
	projected = saturatingAdd(projected, typedExclusionSetBytes(1))

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: projected + extraPayload/2}
	_, err = callHashMember(t, exec, receiver, "except", args, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)

	roomy := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: projected + extraPayload + 64*1024}
	out, err := callHashMember(t, roomy, receiver, "except", args, NewNil())
	if err != nil {
		t.Fatalf("typed except within an ample quota failed: %v", err)
	}
	if got := out.HashLen(); got != 0 {
		t.Fatalf("typed except kept %d entries, want 0", got)
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
	exec.memoryQuota = base + estimatedEmptyOutputHashBytes +
		capacity*estimatedMapEntryStructuralBytes + 3*payloadBytes

	// Mirror a transform that preallocates make(map, capacity): the full output map
	// is reserved through reserveLoopScratch before the accumulator is built, so the
	// accumulator's baseline already accounts for it and add charges only each block
	// result's payload.
	delta := exec.reserveLoopScratch(hashTransformBufferBytes(capacity, 0))
	defer exec.releaseLoopScratch(delta)
	acc := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())

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

	// A quota that admits exactly one full entry and a second entry's cost on top of
	// the accumulator baseline, but no more. The second entry costs more than the
	// scratch, so without the scratch reservation the leftover headroom (a full
	// entry's worth) covers it; once the scratch is reserved only the scratch's worth
	// of headroom remains, which is too small. probe.base is the call-root baseline
	// (the empty output map and scratch are folded in by the caller's
	// reserveLoopScratch, not by the accumulator).
	exec.memoryQuota = probe.base + builtOneEntry + secondEntryCost

	// Without reserving the scratch, both entries fit the budget, proving the
	// reservation -- not the entry payloads alone -- is what rejects the second.
	unreserved := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	if err := unreserved.add(NewString(strings.Repeat("x", payloadBytes))); err != nil {
		t.Fatalf("first entry tripped the quota without a scratch reservation: %v", err)
	}
	if err := unreserved.add(NewString(strings.Repeat("y", payloadBytes))); err != nil {
		t.Fatalf("test setup expects two entries to fit without the scratch reservation: %v", err)
	}

	// Reserving the scratch through reserveLoopScratch before building the
	// accumulator consumes the headroom the second entry relied on, so the first
	// entry fits but the second is rejected.
	delta := exec.reserveLoopScratch(scratchBytes)
	defer exec.releaseLoopScratch(delta)
	reserved := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
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

	// Measure the call-root baseline and one result's payload under an ample quota so
	// the budget is sized from the real accounting rather than a hand-derived estimate.
	probe := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	rootBase := probe.base
	if err := probe.add(NewString(strings.Repeat("x", payloadBytes))); err != nil {
		t.Fatalf("probe result tripped under an unbounded quota: %v", err)
	}
	resultPayload := probe.built

	// A quota that admits the call-root baseline, the one result's payload, and an
	// output map of a single backing slot -- but not the full capacity backing. The
	// early result fits when only one slot is reserved (the buggy view) yet overflows
	// once the whole capacity backing is charged (the fixed view).
	exec.memoryQuota = rootBase + hashTransformBufferBytes(1, 0) + resultPayload

	// Reserving only a single backing slot (the buggy view) wrongly admits the early
	// result: the running budget sees only one slot, not the live capacity backing.
	buggyDelta := exec.reserveLoopScratch(hashTransformBufferBytes(1, 0))
	unreserved := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	if err := unreserved.add(NewString(strings.Repeat("x", payloadBytes))); err != nil {
		exec.releaseLoopScratch(buggyDelta)
		t.Fatalf("with only one slot reserved the early result should be wrongly admitted, got %v", err)
	}
	exec.releaseLoopScratch(buggyDelta)

	// Reserving the full capacity output map first -- exactly what the transform's
	// make(map, capacity) allocates -- rejects the same early result, since the backing
	// plus the result already exceeds the quota.
	fixedDelta := exec.reserveLoopScratch(hashTransformBufferBytes(capacity, 0))
	defer exec.releaseLoopScratch(fixedDelta)
	reserved := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	err := reserved.add(NewString(strings.Repeat("x", payloadBytes)))
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashBuildAccumulatorBackingReservationMatchesProjection pins that the output
// map reserved through reserveLoopScratch equals exactly what the up-front
// projection charges for the same capacity, so the running budget and the preflight
// reserve the same bytes. A drift between the two views would either reject builds
// the projection admitted or admit builds it rejected. The build's final base after
// reserving the output map must equal the call-root usage plus the empty map plus
// capacity structural slots -- the same outputEntries*perEntry the projection adds.
func TestHashBuildAccumulatorBackingReservationMatchesProjection(t *testing.T) {
	t.Parallel()

	const capacity = 4_096
	receiver := largeHashReceiver(capacity)

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	exec.root = newEnv(nil)

	// The projection's baseline (call roots + empty map) plus capacity structural slots
	// is exactly what the accumulator's base must hold once the caller has reserved the
	// output map through reserveLoopScratch, proving the two budgets reserve the same
	// backing. Measure the projection BEFORE reserving the output map, so the
	// reservation does not also fold into projectedHashBaseBytes.
	wantBase := exec.projectedHashBaseBytes(receiver, nil, nil, NewNil()) +
		capacity*estimatedMapEntryStructuralBytes

	delta := exec.reserveLoopScratch(hashTransformBufferBytes(capacity, 0))
	defer exec.releaseLoopScratch(delta)
	acc := newHashBuildAccumulator(exec, receiver, nil, nil, NewNil())

	if acc.base != wantBase {
		t.Fatalf("accumulator base after reserving the output map = %d, want %d (the projection's backing)", acc.base, wantBase)
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
	// Mirror the real transform: it preallocates make(map, count) and reserves that
	// output map through reserveLoopScratch before charging block results, so the
	// probe must reserve it too or the scratch-blind peak would under-count the live
	// backing.
	backingDelta := probe.reserveLoopScratch(hashTransformBufferBytes(count, 0))
	defer probe.releaseLoopScratch(backingDelta)
	acc := newHashBuildAccumulator(probe, receiver, nil, nil, block)
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

func TestHashTransformReservationsRejectBeforeMapAllocation(t *testing.T) {
	t.Parallel()

	const count = 20_000
	receiver := largeHashReceiver(count)

	tests := []struct {
		name  string
		block Value
	}{
		{name: "transform_keys", block: keyIdentityBlock()},
		{name: "transform_values", block: emptyHashBlock()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			probe := &Execution{ctx: context.Background(), quota: 1 << 30}
			roots := probe.hashCallRootBytes(receiver, nil, nil, tt.block)
			buffers := hashTransformBufferBytes(count, sortedKeyBufferBytes(count))
			quota := saturatingAdd(roots, buffers) - 1
			if roots > quota {
				t.Fatalf("test setup expects roots (%d) to fit quota (%d)", roots, quota)
			}

			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
			_, err := callHashMember(t, exec, receiver, tt.name, nil, tt.block)
			requireErrorIs(t, err, errMemoryQuotaExceeded)
			if exec.steps != 0 {
				t.Fatalf("%s ran %d step(s), want the output backing rejected before allocation", tt.name, exec.steps)
			}
			if exec.reservedScratchBytes != 0 {
				t.Fatalf("%s leaked %d reserved scratch bytes after rejection", tt.name, exec.reservedScratchBytes)
			}
		})
	}
}

func TestTypedHashTransformsReserveTypedOutputMap(t *testing.T) {
	t.Parallel()

	const count = 2_000
	receiver := largeTypedSymbolHash(count)
	scratch := sortedHashEntryBufferBytes(count)
	legacyBuffers := hashTransformBufferBytes(count, scratch)
	typedBuffers := typedHashTransformBufferBytes(count, scratch)
	if legacyBuffers >= typedBuffers {
		t.Fatalf("test setup expects typed buffers (%d) to exceed legacy buffers (%d)", typedBuffers, legacyBuffers)
	}

	tests := []struct {
		name  string
		block Value
	}{
		{name: "transform_keys", block: keyIdentityBlock()},
		{name: "transform_values", block: emptyHashBlock()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			probe := &Execution{ctx: context.Background(), quota: 1 << 30}
			roots := probe.hashCallRootBytes(receiver, nil, nil, tt.block)
			quota := roots + legacyBuffers
			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
			_, err := callHashMember(t, exec, receiver, tt.name, nil, tt.block)
			requireErrorIs(t, err, errMemoryQuotaExceeded)
			if exec.steps != 0 {
				t.Fatalf("hash.%s ran %d step(s), want typed output backing rejected before iteration", tt.name, exec.steps)
			}
			if exec.reservedScratchBytes != 0 {
				t.Fatalf("hash.%s leaked %d reserved scratch bytes after rejection", tt.name, exec.reservedScratchBytes)
			}
		})
	}
}

func TestHashTransformReservationsDoNotMaskRequiredBlock(t *testing.T) {
	t.Parallel()

	const count = 20_000
	receiver := largeHashReceiver(count)
	probe := &Execution{ctx: context.Background(), quota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, NewNil())
	buffers := hashTransformBufferBytes(count, sortedKeyBufferBytes(count))
	quota := saturatingAdd(roots, buffers) - 1
	if roots > quota {
		t.Fatalf("test setup expects roots (%d) to fit quota (%d)", roots, quota)
	}

	for _, name := range []string{"transform_keys", "transform_values"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
			_, err := callHashMember(t, exec, receiver, name, nil, NewNil())
			if errors.Is(err, errMemoryQuotaExceeded) {
				t.Fatalf("%s returned memory quota before validating its block: %v", name, err)
			}
			requireErrorContains(t, err, "hash."+name+" requires a block")
			if exec.steps != 0 {
				t.Fatalf("%s ran %d step(s), want required-block validation before iteration", name, exec.steps)
			}
			if exec.reservedScratchBytes != 0 {
				t.Fatalf("%s leaked %d reserved scratch bytes after required-block validation", name, exec.reservedScratchBytes)
			}
		})
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
	receiver := typedSymbolHash("x", NewInt(0))

	// The live footprint is the receiver, the colliding argument hashes (each a
	// one-key map of a small int), and the conservative output charge: one full entry
	// per collision (the block result is charged once per write).
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	liveArgs := make([]Value, collisions)
	for i := range liveArgs {
		liveArgs[i] = typedSymbolHash("x", NewInt(int64(i+1)))
	}
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, liveArgs, nil, NewNil())
	entryBytes := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	// The conservative footprint: a full payload entry per collision, since each
	// block result is charged once when it is written.
	conservative := collisions * (entryBytes + payloadBytes)

	// A quota above the conservative footprint admits the merge.
	roomy := liveWithRoots + estimatedEmptyOutputHashBytes + conservative + 64*1024
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
	tight := liveWithRoots + estimatedEmptyOutputHashBytes + payloadBytes + 64*entryBytes + 64*1024
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
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, NewNil())
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
			exec.memoryQuota = base + estimatedEmptyOutputHashBytes + 64*1024

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

func TestHashTransformKeysChargesRetainedTypedKeyValues(t *testing.T) {
	t.Parallel()

	const count = 2_000
	const keyWidth = 16
	receiver := largeHashReceiver(count)
	block := freshArrayKeyBlockValue(keyWidth)

	arrayKey := func(key string) Value {
		elements := make([]Value, 0, keyWidth+1)
		for i := range keyWidth {
			elements = append(elements, NewInt(int64(i)))
		}
		elements = append(elements, NewSymbol(key))
		return NewArray(elements)
	}
	displayOnlyPayload := 0
	typedPayload := 0
	maxTypedKeyCharge := 0
	for i := range count {
		key := arrayKey("k" + strconv.Itoa(i))
		lookupKey, err := hashLookupKey(key)
		if err != nil {
			t.Fatalf("hashLookupKey(sample array key %d) error = %v, want nil", i, err)
		}
		displayKey := hashDisplayKey(key)
		displayOnlyPayload = saturatingAdd(displayOnlyPayload, newMemoryEstimator().stringPayloadSize(displayKey))
		est := newMemoryEstimator()
		typedKeyCharge := est.valuePayload(key)
		typedKeyCharge = saturatingAdd(typedKeyCharge, est.stringPayloadSize(displayKey))
		typedKeyCharge = saturatingAdd(typedKeyCharge, lookupKey.ExtraPayloadBytes())
		typedPayload = saturatingAdd(typedPayload, typedKeyCharge)
		if typedKeyCharge > maxTypedKeyCharge {
			maxTypedKeyCharge = typedKeyCharge
		}
	}
	if typedPayload <= displayOnlyPayload {
		t.Fatalf("test setup expects typed key payload (%d) to exceed display-only payload (%d)", typedPayload, displayOnlyPayload)
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	buffers := typedHashTransformBufferBytes(count, sortedKeyBufferBytes(count))
	transientKeyPayload := maxTypedKeyCharge
	payloadHeadroom := displayOnlyPayload
	if transientKeyPayload > payloadHeadroom {
		payloadHeadroom = transientKeyPayload
	}
	if typedPayload <= payloadHeadroom {
		t.Fatalf("test setup expects retained typed keys (%d) to exceed payload headroom (%d)", typedPayload, payloadHeadroom)
	}

	quota := saturatingAdd(saturatingAdd(roots, buffers), payloadHeadroom)
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "transform_keys", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)

	roomy := saturatingAdd(saturatingAdd(roots, buffers), typedPayload)
	roomy = saturatingAdd(roomy, maxTypedKeyCharge)
	exec = &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: roomy}
	got, err := callHashMember(t, exec, receiver, "transform_keys", nil, block)
	if err != nil {
		t.Fatalf("hash.transform_keys with retained typed array keys under roomy quota error = %v, want nil", err)
	}
	if got.HashLen() != count {
		t.Fatalf("hash.transform_keys with retained typed array keys produced %d entries, want %d", got.HashLen(), count)
	}
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
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, NewNil())
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	outputStructure := estimatedEmptyOutputHashBytes + count*perEntry
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

func keyIdentityBlock() Value {
	pos := Position{Line: 1, Column: 1}
	return NewBlock(
		[]Param{{Kind: ParamNormal, Name: "k", Target: &Identifier{Name: "k", Position: pos}}},
		[]Statement{&ExprStmt{Position: pos, Expr: &Identifier{Name: "k", Position: pos}}},
		newEnv(nil),
	)
}

func freshArrayKeyBlockValue(width int) Value {
	pos := Position{Line: 1, Column: 1}
	elements := make([]Expression, 0, width+1)
	for i := range width {
		elements = append(elements, &IntegerLiteral{Value: int64(i), Position: pos})
	}
	elements = append(elements, &Identifier{Name: "k", Position: pos})
	return NewBlock(
		[]Param{{Kind: ParamNormal, Name: "k", Target: &Identifier{Name: "k", Position: pos}}},
		[]Statement{&ExprStmt{Expr: &ArrayLiteral{Elements: elements, Position: pos}, Position: pos}},
		newEnv(nil),
	)
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

func TestHashIteratorsStopWhenBlockCancelsContext(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "each pair",
			source: `def run(values)
  values.each do |pair|
    cancel_now(pair)
  end
end`,
		},
		{
			name: "each key value",
			source: `def run(values)
  values.each do |key, value|
    cancel_now(value)
  end
end`,
		},
		{
			name: "each_with_index",
			source: `def run(values)
  values.each_with_index do |pair, index|
    cancel_now(pair)
  end
end`,
		},
		{
			name: "each_key",
			source: `def run(values)
  values.each_key do |key|
    cancel_now(key)
  end
end`,
		},
		{
			name: "each_value",
			source: `def run(values)
  values.each_value do |value|
    cancel_now(value)
  end
end`,
		},
		{
			name: "merge conflict block",
			source: `def run(values)
  values.merge({ "k0": 99 }) do |key, old_value, new_value|
    cancel_now(new_value)
  end
end`,
		},
		{
			name: "select",
			source: `def run(values)
  values.select do |key, value|
    cancel_now()
  end
end`,
		},
		{
			name: "reject",
			source: `def run(values)
  values.reject do |key, value|
    cancel_now()
  end
end`,
		},
		{
			name: "map_with_index",
			source: `def run(values)
  values.map_with_index do |pair, index|
    cancel_now(pair)
  end
end`,
		},
		{
			name: "transform_keys",
			source: `def run(values)
  values.transform_keys do |key|
    cancel_now(key)
  end
end`,
		},
		{
			name: "transform_values",
			source: `def run(values)
  values.transform_values do |value|
    cancel_now(value)
  end
end`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var cancel context.CancelFunc
			calls := 0
			engine := MustNewEngine(Config{StepQuota: 10_000_000, MemoryQuotaBytes: 64 << 20})
			engine.builtins["cancel_now"] = NewBuiltin("cancel_now", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
				calls++
				cancel()
				if len(args) > 0 {
					return args[0], nil
				}
				return NewBool(true), nil
			})
			script := compileScriptWithEngine(t, engine, tc.source)
			ctx, cancelFunc := context.WithCancel(context.Background())
			cancel = cancelFunc

			_, err := script.Call(ctx, "run", []Value{largeHashReceiver(2)}, CallOptions{})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s after block-canceled context = %v, want context.Canceled", tc.name, err)
			}
			if calls != 1 {
				t.Fatalf("%s cancel_now calls = %d, want 1", tc.name, calls)
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
	base := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, NewNil())
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

func TestForHashReachablePairBodyFitsWithoutPairReservation(t *testing.T) {
	t.Parallel()

	const bodyLen = 20_000
	key := strings.Repeat("k", 20_000)
	receiver := NewHash(map[string]Value{key: NewInt(1)})
	body := arrayValue(bodyLen)
	pair := NewArray([]Value{NewSymbol(key), NewInt(1)})
	scratch := sortedKeyBufferBytes(1)
	pairChargeProbe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	pairCharge := pairChargeProbe.maxCollapsedPairBytes(receiver)

	peakEnv := newEnv(nil)
	peakEnv.Assign("values", receiver)
	peakEnv.Assign("body", body)
	peakEnv.Assign("pair", pair)
	peakProbe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	peakProbe.reservedScratchBytes = scratch
	peakProbe.pushEnv(peakEnv)
	bodyPeak := peakProbe.estimateMemoryUsage(body)
	peakProbe.popEnv()

	preflightEnv := newEnv(nil)
	preflightEnv.Assign("values", receiver)
	preflightEnv.Assign("body", body)
	preflightProbe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	preflightProbe.reservedScratchBytes = scratch
	preflightProbe.pushEnv(preflightEnv)
	preflightEst := preflightProbe.memoryEstimatorForCheck()
	preflightPeak := preflightProbe.estimateMemoryUsageBase(preflightEst)
	preflightPeak = saturatingAdd(preflightPeak, maxCollapsedPairBytesWithEstimator(receiver, preflightEst))
	preflightProbe.popEnv()

	realPeak := max(bodyPeak, preflightPeak)
	quota := realPeak + pairCharge/2
	if pairCharge <= 0 {
		t.Fatalf("test setup expects a positive pair charge, got %d", pairCharge)
	}

	pos := Position{Line: 1, Column: 1}
	stmt := &ForStmt{
		Iterator: "pair",
		Body:     []Statement{&ExprStmt{Position: pos, Expr: &Identifier{Name: "body", Position: pos}}},
		Position: pos,
	}
	loopEnv := newEnv(nil)
	loopEnv.Assign("values", receiver)
	loopEnv.Assign("body", body)
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	exec.pushEnv(loopEnv)
	_, _, err := exec.evalForHash(stmt, loopEnv, receiver, NewNil())
	exec.popEnv()
	if err != nil {
		t.Fatalf("for pair in hash with reachable iterable and bound pair at quota %d = %v, want success", quota, err)
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
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, []Value{arg}, nil, NewNil())

	// A quota above the receiver's footprint plus the merge's one-entry output map, so
	// the structural projection passes and the receiver array fits, but well below that
	// plus the fresh payload, so charging the conflict result's full footprint trips.
	outputStructure := estimatedEmptyOutputHashBytes +
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
// The test drives the accumulator exactly as merge does (reserve the output map
// through reserveLoopScratch, then add per conflict result) under two reservations.
// Reserving the union (the fix) rejects the first conflict result, because the grown
// backing plus the result overflows the quota. Reserving only len(base) (the pre-fix
// bug) wrongly admits it, since the under-reserved backing leaves headroom the live
// map has already consumed. The gap between the two is exactly the
// (union - len(base)) slots the non-conflict additions grow.
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

	// Learn the accumulator baseline (call roots) under no real pressure, matching
	// what merge snapshots before reserving any backing.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1}
	probe.root = newEnv(nil)
	probeAcc := newHashBuildAccumulator(probe, NewNil(), nil, nil, NewNil())
	base := probeAcc.base
	resultBytes := newMemoryEstimator().valuePayload(result)

	// Size the quota to fit the union output map plus the first result short by one
	// byte, so reserving the union and then adding the result trips, while reserving
	// only len(base) leaves room the union-grown map has actually consumed.
	quota := base + hashTransformBufferBytes(unionLen, 0) + resultBytes - 1

	// Sanity: a len(base)-sized backing plus the same result must fit the quota, so
	// the pre-fix reservation would wrongly admit the result.
	if base+hashTransformBufferBytes(baseLen, 0)+resultBytes > quota {
		t.Fatalf("test setup expects base-len backing + result (%d) to fit the quota (%d)",
			base+hashTransformBufferBytes(baseLen, 0)+resultBytes, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	exec.root = newEnv(nil)

	// The fix: reserving the union output map, then the first conflict result trips,
	// because the grown backing already consumed the quota's headroom.
	fixedDelta := exec.reserveLoopScratch(hashTransformBufferBytes(unionLen, 0))
	fixed := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	if err := fixed.add(result); !errors.Is(err, errMemoryQuotaExceeded) {
		exec.releaseLoopScratch(fixedDelta)
		t.Fatalf("union-backed accumulator admitted the first conflict result, want rejection: %v", err)
	}
	exec.releaseLoopScratch(fixedDelta)

	// The pre-fix bug: reserving only len(base) leaves headroom the union-grown map
	// has already consumed, so the same result is wrongly admitted.
	buggyDelta := exec.reserveLoopScratch(hashTransformBufferBytes(baseLen, 0))
	defer exec.releaseLoopScratch(buggyDelta)
	buggy := newHashBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
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
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, args, nil, NewNil())
	emptyMap := estimatedEmptyOutputHashBytes
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
	liveWithRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, args, nil, block)
	emptyMap := estimatedEmptyOutputHashBytes
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

// overFixedNestedRestPairTarget builds the destructure target |(a, b, c, *(x))|:
// three fixed elements followed by a rest target that is itself a destructure. Over
// a collapsed two-element [key, value] pair the rest index (3) exceeds the value
// count (2), the shape that made AssignDestructure slice values[restIndex:] out of
// range and panic the host before it clamped the low bound to len(values).
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

// overFixedNestedRestPairBlock builds an |(a, b, c, *(x))| block with an EMPTY body.
// blockWantsCollapsedPair stays true (one positional parameter), so Hash#each collapses
// each entry into a [key, value] pair and binds it through this over-fixed nested
// rest shape -- the shape whose rest reconstruction panicked the host before
// AssignDestructure clamped the rest window's low bound.
func overFixedNestedRestPairBlock() Value {
	params := []Param{{Kind: ParamNormal, Target: overFixedNestedRestPairTarget()}}
	return NewBlock(params, nil, newEnv(nil))
}

// TestHashEachOverFixedNestedRestDoesNotPanic is the end-to-end twin of the P1
// regression: iterating a hash with an |(a, b, c, *(x))| block under a memory quota
// must complete without panicking the host. Each entry's collapsed pair holds only
// two values, fewer than the three fixed targets plus the nested rest, so
// AssignDestructure's rest window slices out of range unless its low bound is clamped
// to len(values). The walk binds the missing fixed targets to nil and an empty rest,
// and returns the receiver.
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

	// A real but generous quota so the memory path -- including the per-entry pair
	// charge and the in-block destructure -- runs under enforcement rather than the
	// no-quota short circuit. The receiver and its small pairs fit comfortably.
	quota := roots + scratch + 64*1024
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("each over |(a, b, c, *(x))| at quota %d = %v, want success without panicking", quota, err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != len(receiver.Hash()) {
		t.Fatalf("each returned %v with %d entries, want the %d-entry receiver", got.Kind(), len(got.Hash()), len(receiver.Hash()))
	}
}

// arrayValue builds an array of length ints for the reusable-env rest tests.
func arrayValue(length int) Value {
	elems := make([]Value, length)
	for i := range elems {
		elems[i] = NewInt(int64(i))
	}
	return NewArray(elems)
}

// recordingRestEachBlock builds the nested-rest |(k, (head, *tail))| block whose
// body records the bound tail's length into the given slice on every call. The body
// references only the parameter binding and a captured recorder (no definitions or
// closures over the current env), so the block keeps its reusable environment, the
// path on which the previous iteration's rest binding can linger.
func recordingRestEachBlock(recorder *[]int) Value {
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
	record := NewBuiltin("test.record_tail", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		*recorder = append(*recorder, len(args[0].Array()))
		return NewNil(), nil
	})
	env := newEnv(nil)
	env.Define("__record__", record)
	body := []Statement{
		&ExprStmt{Position: pos, Expr: &CallExpr{
			Position: pos,
			Callee:   &Identifier{Name: "__record__", Position: pos},
			Args:     []Expression{&Identifier{Name: "tail", Position: pos}},
		}},
	}
	return NewBlock([]Param{{Kind: ParamNormal, Target: target}}, body, env)
}

// TestHashEachBindsEachEntryOwnRest proves a reused block environment does not leak a
// stale rest binding across iterations: each entry's block call binds and sees that
// entry's own live rest, never the previous iteration's. The reusable-env path (the
// block defines nothing and captures nothing, so blockCanReuseEnv holds) is exactly
// where a prior call's tail binding could linger; runner.call resets the environment
// before binding each call's parameters, so every call binds its own value. The walk
// records each bound tail's length and the recorded lengths must equal each entry's
// value length minus one (the head), in sorted-key order.
func TestHashEachBindsEachEntryOwnRest(t *testing.T) {
	t.Parallel()

	const hugeTailLen = 50_000

	receiver := NewHash(map[string]Value{
		"a": arrayValue(hugeTailLen),
		"b": NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		"c": NewArray([]Value{NewInt(1), NewInt(2)}),
	})

	var recorded []int
	block := recordingRestEachBlock(&recorded)
	if !blockCanReuseEnv(valueBlock(block)) {
		t.Fatal("test setup expects a reusable-env block so the cross-iteration rebind path is exercised")
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("each over the recording nested-rest block = %v, want success", err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != 3 {
		t.Fatalf("each returned %v with %d entries, want the 3-entry receiver", got.Kind(), len(got.Hash()))
	}

	// Sorted-key order a < b < c; each tail is its value length minus the head. If a
	// call bound a value the body still needed wrong, or left a stale tail in place,
	// these lengths would not match each entry's own live value.
	want := []int{hugeTailLen - 1, 2, 1}
	if len(recorded) != len(want) {
		t.Fatalf("recorded %d tail lengths %v, want %d (%v)", len(recorded), recorded, len(want), want)
	}
	for i := range want {
		if recorded[i] != want[i] {
			t.Fatalf("recorded tail lengths = %v, want %v (entry %d bound the wrong tail)", recorded, want, i)
		}
	}
}

// emptyNestedRestEachBlock builds an EMPTY-body |(k, (head, *tail))| block. The
// block defines and captures nothing, so it keeps its reusable environment and runs
// no body statements -- the case where the body's own per-statement memory checks
// never observe the freshly bound tail.
func emptyNestedRestEachBlock() Value {
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
	return NewBlock([]Param{{Kind: ParamNormal, Target: target}}, nil, newEnv(nil))
}

// nestedRestTailChargeBytes returns the bytes the per-call bind charge attributes to
// the fresh tail a |(k, (head, *tail))| block binds over a [key, value] pair whose
// value is valueArray. It mirrors blockBindCharge exactly: seed a fresh estimator
// with the yielded pair (so payloads shared with the value deduplicate), then charge
// the genuinely fresh rest backing of valueArray[1:]. Tests size quotas from this so
// they track the estimator's real accounting rather than a hand-derived structure
// that could drift from how arrays are charged.
func nestedRestTailChargeBytes(valueArray Value) int {
	pair := NewArray([]Value{NewSymbol("a"), valueArray})
	tail := NewArray(slicesClone(valueArray.Array()[1:]))
	est := newMemoryEstimator()
	est.value(pair)
	return est.value(tail)
}

// slicesClone copies src into a fresh backing of capacity exactly len(src), matching
// the make+copy AssignDestructure uses for a rest backing so the charged footprint is
// identical to the runtime path.
func slicesClone(src []Value) []Value {
	out := make([]Value, len(src))
	copy(out, src)
	return out
}

// TestHashEachEmptyBodyNestedRestTripsMemoryQuota is the core regression for the
// nested-rest sandbox escape on PR #808. Hash#each yields each entry as a bounded
// two-element [key, value] pair, but |(k, (head, *tail))| binds tail to a FRESH
// copy of the whole hash value -- a backing sized to the value, not to the pair.
// With an EMPTY block body the body's own memory checks never run, and the receiver
// lives only on the Go call stack (invisible to estimateMemoryUsageBase), so the
// fresh tail copy could escape the quota entirely. The per-call bind charge counts
// that copy against the live call roots (including the receiver), so a quota that
// admits the receiver and the bounded pair but NOT the fresh tail copy must reject
// the walk. Without the charge this test fails: the empty body binds the huge tail
// and returns without ever tripping.
func TestHashEachEmptyBodyNestedRestTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// One entry whose value is a large array, so the rest copy AssignDestructure
	// allocates dwarfs the bounded pair the iterator reserves.
	const valueLen = 200_000
	value := arrayValue(valueLen)
	receiver := NewHash(map[string]Value{"a": value})
	block := emptyNestedRestEachBlock()
	if !blockCanReuseEnv(valueBlock(block)) {
		t.Fatal("test setup expects a reusable-env block so the empty-body rebind path is exercised")
	}

	// Size the quota to admit the live call roots (which include the receiver and so
	// the value array) plus the reserved [key, value] pair and a little headroom, but
	// NOT the fresh tail copy the nested rest allocates.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	tailCharge := nestedRestTailChargeBytes(value)
	pairCharge := probe.maxCollapsedPairBytes(receiver)
	const headroom = 16 * 1024
	quota := roots + pairCharge + headroom

	// Sanity: the fresh tail copy genuinely exceeds the headroom the quota leaves
	// above the receiver, so its omission -- not an incidentally tight quota -- is
	// what would let the walk escape.
	if tailCharge <= headroom+pairCharge {
		t.Fatalf("test setup expects the tail charge (%d) to exceed the headroom above the roots (%d)", tailCharge, headroom+pairCharge)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "each", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashEachNestedRestFitsWhenTailFitsQuota pins the other side of the bind
// charge: it must not over-reject a nested-rest walk whose fresh tail copy genuinely
// fits. A quota generously above the receiver, the pair, and the tail copy admits
// the empty-body walk and returns the receiver, proving the charge bounds only the
// fresh backing rather than rejecting every rest-collecting block.
func TestHashEachNestedRestFitsWhenTailFitsQuota(t *testing.T) {
	t.Parallel()

	const valueLen = 50_000
	value := arrayValue(valueLen)
	receiver := NewHash(map[string]Value{"a": value})
	block := emptyNestedRestEachBlock()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	tailCharge := nestedRestTailChargeBytes(value)
	pairCharge := probe.maxCollapsedPairBytes(receiver)
	quota := roots + pairCharge + tailCharge + 64*1024

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("each over a nested-rest block whose tail fits the quota = %v, want success", err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != 1 {
		t.Fatalf("each returned %v with %d entries, want the 1-entry receiver", got.Kind(), len(got.Hash()))
	}
}

// emptyPairEachBlock builds an EMPTY-body single-parameter |pair| block. Its lone
// positional parameter binds the whole [key, value] pair by reference (no
// destructuring, no rest), so it triggers the collapsed-pair yield and allocates
// nothing beyond the pair the iterator builds. The empty body means no per-statement
// body check ever runs, so the pair reservation is the only thing that can bound the
// transient pair against the Go-stack-only receiver.
func emptyPairEachBlock() Value {
	pos := Position{Line: 1, Column: 1}
	return NewBlock([]Param{{Kind: ParamNormal, Name: "pair", Target: &Identifier{Name: "pair", Position: pos}}}, nil, newEnv(nil))
}

func bodyPairEachBlock(env *Env) Value {
	pos := Position{Line: 1, Column: 1}
	return NewBlock(
		[]Param{{Kind: ParamNormal, Name: "pair", Target: &Identifier{Name: "pair", Position: pos}}},
		[]Statement{&ExprStmt{Position: pos, Expr: &Identifier{Name: "body", Position: pos}}},
		env,
	)
}

// collapsedPairStructuralBytes is the structural-only footprint a fixed pair
// constant would reserve: the array Value, its slice base, and two element slots,
// with no payload for the symbol key or the value. The pre-fix code reserved this
// constant, which omitted the symbol key's string payload the estimator bills, so a
// quota between receiver+structure and receiver+full-pair escaped the sandbox.
const collapsedPairStructuralBytes = estimatedValueBytes + estimatedSliceBaseBytes + 2*estimatedValueBytes

// TestHashEachSingleParamLargeSymbolKeyTripsMemoryQuota is the P2 regression for the
// pair reservation omitting the symbol key's payload. A single-parameter |pair| block
// yields each entry as a transient [NewSymbol(key), value] pair; the estimator bills
// the symbol key's string header and payload on top of the array structure. With the
// receiver held only on the iterator's Go frame, the body check (here, an empty body
// that never runs one) cannot see it, so the pair reservation in the iterator
// preflight is the sole guard combining the receiver with the live pair. A structural
// constant omitting the symbol payload let a quota between receiver+structure and
// receiver+full-pair pass that preflight and escape. Reserving the exact pair size
// (maxCollapsedPairBytes), which probes the real symbol-keyed pair, rejects it.
func TestHashEachSingleParamLargeSymbolKeyTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// One entry whose KEY is a large symbol so the symbol payload the structural
	// constant omitted dominates the gap, with a tiny value so the pair's cost is the
	// key, not the value.
	bigKey := strings.Repeat("k", 200_000)
	receiver := NewHash(map[string]Value{bigKey: NewInt(1)})
	block := emptyPairEachBlock()
	if !blockWantsCollapsedPair(valueBlock(block)) {
		t.Fatal("test setup expects a single-parameter block that yields the collapsed pair")
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	pairCharge := probe.maxCollapsedPairBytes(receiver)

	// Sanity: the real pair charge genuinely exceeds the structural constant by the
	// symbol payload, so the omission -- not an incidentally tight quota -- is what
	// let the walk escape.
	if pairCharge <= collapsedPairStructuralBytes {
		t.Fatalf("test setup expects the real pair charge (%d) to exceed the structural constant (%d) by the symbol payload", pairCharge, collapsedPairStructuralBytes)
	}

	// A quota that admits the receiver and the structural constant but NOT the real
	// pair. The pre-fix reservation (structural constant) fit this and let the empty
	// body walk the hash and escape; the exact reservation rejects it.
	quota := roots + collapsedPairStructuralBytes + (pairCharge-collapsedPairStructuralBytes)/2

	// Sanity: the pre-fix structural reservation genuinely fit the quota, proving the
	// symbol payload -- not an over-tight quota -- is the deciding term.
	if roots+collapsedPairStructuralBytes > quota {
		t.Fatalf("test setup expects roots+structure (%d) to fit the quota (%d) so the omitted symbol payload is the deciding term", roots+collapsedPairStructuralBytes, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "each", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashEachSingleParamLargeSymbolKeyFitsQuota pins the other side: a quota
// generously above the receiver and the full symbol-keyed pair must NOT over-reject
// the collapsed-pair walk. It admits the empty-body walk and returns the receiver,
// proving the exact pair reservation bounds only the transient pair rather than
// rejecting every single-parameter block over a large-keyed hash.
func TestHashEachSingleParamLargeSymbolKeyFitsQuota(t *testing.T) {
	t.Parallel()

	bigKey := strings.Repeat("k", 50_000)
	receiver := NewHash(map[string]Value{bigKey: NewInt(1)})
	block := emptyPairEachBlock()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	pairCharge := probe.maxCollapsedPairBytes(receiver)
	quota := roots + pairCharge + 64*1024

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "each", nil, block)
	if err != nil {
		t.Fatalf("each over a single-parameter block whose pair fits the quota = %v, want success", err)
	}
	if got.Kind() != KindHash || len(got.Hash()) != 1 {
		t.Fatalf("each returned %v with %d entries, want the 1-entry receiver", got.Kind(), len(got.Hash()))
	}
}

func TestHashEachReachablePairBodyFitsWithoutPairReservation(t *testing.T) {
	t.Parallel()

	const bodyLen = 20_000
	key := strings.Repeat("k", 20_000)
	receiver := NewHash(map[string]Value{key: NewInt(1)})
	body := arrayValue(bodyLen)
	parent := newEnv(nil)
	parent.Assign("values", receiver)
	parent.Assign("body", body)
	block := bodyPairEachBlock(parent)

	pair := NewArray([]Value{NewSymbol(key), NewInt(1)})
	scratch := sortedKeyBufferBytes(1)
	pairChargeProbe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	pairCharge := pairChargeProbe.maxCollapsedPairBytes(receiver)

	bodyEnv := newEnv(parent)
	bodyEnv.Assign("pair", pair)
	peakProbe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	peakProbe.reservedScratchBytes = scratch
	peakProbe.pushEnv(bodyEnv)
	bodyPeak := peakProbe.estimateMemoryUsage(body)
	peakProbe.popEnv()

	preflightProbe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	preflightProbe.reservedScratchBytes = scratch
	preflightEst := preflightProbe.memoryEstimatorForCheck()
	preflightPeak := preflightProbe.estimateMemoryUsageBase(preflightEst)
	preflightPeak = saturatingAdd(preflightPeak, preflightEst.value(block))
	preflightPeak = saturatingAdd(preflightPeak, maxCollapsedPairBytesWithEstimator(receiver, preflightEst))

	realPeak := max(bodyPeak, preflightPeak)
	quota := realPeak + pairCharge/2
	if pairCharge <= 0 {
		t.Fatalf("test setup expects a positive pair charge, got %d", pairCharge)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	if _, err := callHashMember(t, exec, receiver, "each", nil, block); err != nil {
		t.Fatalf("hash.each with reachable receiver and bound pair at quota %d = %v, want success", quota, err)
	}
}

// TestMaxCollapsedPairBytesIncludesKeySymbolPayload directly pins that the
// collapsed [key, value] pair reservation accounts for the key symbol's payload,
// not just the array structure. The end-to-end quota tests above size their
// quotas from maxCollapsedPairBytes itself, so they cannot detect an omission
// where the reservation and the actual charge drop the symbol together; this
// unit assertion can. A key that is keyLen bytes longer must raise the pair
// reservation by approximately keyLen; if the symbol payload is dropped the
// delta collapses to ~0, which is the exact escape the finding described.
func TestMaxCollapsedPairBytesIncludesKeySymbolPayload(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	const keyLen = 80_000
	small := exec.maxCollapsedPairBytes(NewHash(map[string]Value{"k": NewInt(1)}))
	big := exec.maxCollapsedPairBytes(NewHash(map[string]Value{strings.Repeat("k", keyLen): NewInt(1)}))

	if delta := big - small; delta < keyLen-1 {
		t.Fatalf("maxCollapsedPairBytes omits the key symbol payload: big=%d small=%d delta=%d, want >= ~%d", big, small, delta, keyLen-1)
	}
}

// TestHashEachEmptyBodyNestedRestCountsScratchInBindCharge is the regression for
// the ordering gap the Codex thread on PR #808 raised: the bind-charge baseline
// must be snapshotted AFTER the walk scratch is reserved, not before. Hash#each
// reserves a sorted-key scratch slice plus one collapsed pair for the walk's
// lifetime, then yields each entry to a |(k, (head, *tail))| block whose empty body
// never runs its own memory checks. With the buggy ordering newBlockCallRunner
// snapshotted the bind charge before reserveLoopScratch, so the charge measured the
// fresh tail copy against a baseline that omitted the scratch (and pair). For a
// many-key receiver carrying one large value, the scratch and pair are non-trivial,
// so a quota that admits receiver+scratch (the walk preflight) and receiver+tail
// (the buggy bind charge) SEPARATELY but not the real peak receiver+scratch+pair+tail
// would let the empty body bind the huge tail and escape the quota. Reserving the
// scratch before the runner folds it into the baseline so the charge rejects the
// combined peak. The companion TestHashEachEmptyBodyNestedRestTripsMemoryQuota
// covers the tail itself; this one isolates the scratch's inclusion in the baseline.
func TestHashEachEmptyBodyNestedRestCountsScratchInBindCharge(t *testing.T) {
	t.Parallel()

	// Many keys so the sorted-key scratch buffer is non-trivial, with exactly one
	// entry whose value is large enough that its rest copy dwarfs the scratch+pair;
	// the rest carry tiny values that bind tails the quota easily admits.
	const entryCount = 256
	const bigValueLen = 200_000
	bigValue := arrayValue(bigValueLen)
	entries := make(map[string]Value, entryCount)
	entries["big"] = bigValue
	for i := range entryCount - 1 {
		entries["k"+strconv.Itoa(i)] = arrayValue(2)
	}
	receiver := NewHash(entries)
	block := emptyNestedRestEachBlock()
	if !blockCanReuseEnv(valueBlock(block)) {
		t.Fatal("test setup expects a reusable-env block so the empty-body rebind path is exercised")
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	scratch := sortedKeyBufferBytes(len(entries))
	pairCharge := probe.maxCollapsedPairBytes(receiver)
	tailCharge := nestedRestTailChargeBytes(bigValue)

	// Sanity: the scratch buffer is genuinely non-trivial, so the buggy ordering's
	// omission of it -- not an incidentally tight quota -- is what would let the walk
	// escape.
	if scratch <= 0 {
		t.Fatalf("test setup expects a heaped sorted-key scratch buffer, got %d bytes", scratch)
	}

	// Pick the quota one byte below the real peak (roots+scratch+pair+tail). The
	// correct charge, which now folds the scratch and pair into the baseline, rejects
	// this peak. The buggy charge (baseline roots+tail, scratch omitted) and the walk
	// preflight (roots+scratch, tail not yet bound) each fit it, so only the
	// scratch's inclusion in the baseline catches the overflow.
	quota := roots + scratch + pairCharge + tailCharge - 1

	// The buggy bind charge would have measured only roots+tail, which must fit the
	// quota, proving the scratch (not the tail or an over-tight quota) is the sole
	// reason the corrected charge rejects the walk.
	if roots+tailCharge > quota {
		t.Fatalf("test setup expects roots+tail (%d) to fit the quota (%d) so the scratch is the deciding term", roots+tailCharge, quota)
	}
	// The walk preflight sees roots+scratch (the tail is not bound until the block is
	// called), which must also fit so the preflight does not pre-empt the bind charge.
	if roots+scratch > quota {
		t.Fatalf("test setup expects roots+scratch (%d) to fit the quota (%d) so the preflight does not pre-empt the bind charge", roots+scratch, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "each", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashEachNestedRestAccumulatorTripsMemoryQuota covers the live-footprint shape
// the Codex thread on PR #808 raised: a block body that grows an outer accumulator
// from each bound tail keeps earlier tails live, so binding a later entry's tail
// allocates on top of every prior tail still referenced. With the receiver bound to a
// script parameter (so it is reachable from the call env, not just the Go stack), the
// body's own per-statement memory checks see both the growing accumulator and the
// freshly bound tail in the reused/captured env chain, so the retained tails must
// trip the quota partway through the walk rather than after the whole hash is
// processed. This guards that retaining bound rests is bounded over the iteration,
// not just per entry.
func TestHashEachNestedRestAccumulatorTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// acc = acc + [tail] retains every bound tail through the outer accumulator, so
	// the live footprint climbs by each entry's tail as the walk proceeds. The block
	// captures acc, so it does not reuse its env, mirroring real closure-bearing
	// blocks where the bound rest persists.
	source := `def run(values)
  acc = []
  values.each do |(k, (head, *tail))|
    acc = acc + [tail]
  end
  acc
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 16 << 20}, source)

	// Several entries each carrying a sizable value, so the accumulator's retained
	// tails climb past the 16 MiB quota partway through the walk.
	const entries = 64
	const valueLen = 20_000
	receiverEntries := make(map[string]Value, entries)
	for i := range entries {
		receiverEntries["k"+strconv.Itoa(i)] = arrayValue(valueLen)
	}
	receiver := NewHash(receiverEntries)

	requireCallRuntimeErrorType(t, script, "run", []Value{receiver}, CallOptions{}, runtimeErrorTypeLimit)
}

// emptyMergeConflictNestedRestBlock builds the three-parameter conflict block a
// block-driven Hash#merge yields (key, old_value, new_value) to, with the third
// parameter destructuring new_value into a nested rest |k, old, (head, *tail)| and an
// EMPTY body. Binding tail copies the conflicting argument value into a fresh,
// source-sized backing; the empty body means only the per-call bind charge can
// observe that copy.
func emptyMergeConflictNestedRestBlock() Value {
	pos := Position{Line: 1, Column: 1}
	newValueTarget := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "head", Position: pos}},
			{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
		},
	}
	params := []Param{
		{Kind: ParamNormal, Name: "k", Target: &Identifier{Name: "k", Position: pos}},
		{Kind: ParamNormal, Name: "old", Target: &Identifier{Name: "old", Position: pos}},
		{Kind: ParamNormal, Target: newValueTarget},
	}
	return NewBlock(params, nil, newEnv(nil))
}

// TestHashMergeConflictArgNestedRestTripsMemoryQuota is the regression for the
// positional-call-root gap the Codex thread on PR #808 raised. A block-driven
// Hash#merge holds its argument hashes only on the Go call stack while the conflict
// loop runs; binding a conflicting new_value into a rest-collecting destructure
// (|k, old, (head, *tail)|) copies that argument value into a fresh, source-sized
// backing. With an EMPTY block body only the per-call bind charge observes the copy,
// and that charge must measure it against a baseline that counts the argument hash it
// was copied from. The quota here is sized to admit the argument hash and the fresh
// tail copy SEPARATELY (the buggy baseline, which omitted the positional roots, so
// charged only receiver + tail) but NOT the real peak (receiver + argument + tail).
// Before the fix the buggy baseline let the merge complete and escape the quota; the
// fix counts the argument root so the combined peak is rejected.
func TestHashMergeConflictArgNestedRestTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// The receiver and the argument share key "a" so the conflict block fires. The
	// argument's value is the large array whose tail the nested rest copies; the
	// receiver's value is tiny so the receiver alone leaves ample headroom.
	const valueLen = 200_000
	argValue := arrayValue(valueLen)
	receiver := NewHash(map[string]Value{"a": NewArray([]Value{NewInt(0)})})
	other := NewHash(map[string]Value{"a": argValue})
	args := []Value{other}
	block := emptyMergeConflictNestedRestBlock()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	// The buggy baseline omitted the positional argument roots, so it charged the
	// fresh tail against receiver + block alone. Size the quota to clear that buggy
	// peak with headroom so the bug would have let the merge complete.
	buggyRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	tailCharge := mergeConflictTailChargeBytes(argValue)
	const headroom = 16 * 1024
	quota := buggyRoots + tailCharge + headroom

	// The real peak counts the argument hash too. It must exceed the quota, so the
	// only thing standing between a passing and a rejected merge is whether the bind
	// charge includes the positional argument root.
	correctRoots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, args, nil, block)
	if correctRoots+tailCharge <= quota {
		t.Fatalf("test setup expects the argument-inclusive peak (%d) to exceed the quota (%d); the argument footprint must dwarf the headroom", correctRoots+tailCharge, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "merge", args, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// mergeConflictTailChargeBytes returns the bytes the per-call bind charge attributes
// to the fresh tail a |k, old, (head, *tail)| conflict block binds when new_value is
// valueArray. It mirrors blockBindCharge.begin/charge: seed a fresh estimator with
// the yielded (key, old_value, new_value) arguments (so payloads shared with new_value
// deduplicate), then charge the genuinely fresh rest backing of valueArray[1:].
func mergeConflictTailChargeBytes(valueArray Value) int {
	est := newMemoryEstimator()
	est.value(NewSymbol("a"))
	est.value(NewArray([]Value{NewInt(0)}))
	est.value(valueArray)
	tail := NewArray(slicesClone(valueArray.Array()[1:]))
	return est.value(tail)
}

// emptyValueRestTransformBlock builds the two-parameter |k, (head, *tail)| block a
// block-driven hash transform (select, reject, transform_values) yields (key, value)
// to, with the second parameter destructuring the value into a nested rest and an
// EMPTY body. Two positional parameters opt out of the collapsed-pair yield, so the
// iterator yields key and value separately and the second parameter copies the value
// into a fresh, source-sized rest backing. The empty body means only the per-call
// bind charge can observe that copy.
func emptyValueRestTransformBlock() Value {
	pos := Position{Line: 1, Column: 1}
	valueTarget := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "head", Position: pos}},
			{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
		},
	}
	params := []Param{
		{Kind: ParamNormal, Name: "k", Target: &Identifier{Name: "k", Position: pos}},
		{Kind: ParamNormal, Target: valueTarget},
	}
	return NewBlock(params, nil, newEnv(nil))
}

// valueRestTailChargeBytes returns the bytes the per-call bind charge attributes to
// the fresh tail a |k, (head, *tail)| transform block binds over the (key, value)
// pair whose value is valueArray. It mirrors blockBindCharge.begin/charge: seed a
// fresh estimator with the yielded key and value (so payloads shared with the value
// deduplicate), then charge the genuinely fresh rest backing of valueArray[1:].
func valueRestTailChargeBytes(key string, valueArray Value) int {
	est := newMemoryEstimator()
	est.value(NewSymbol(key))
	est.value(valueArray)
	tail := NewArray(slicesClone(valueArray.Array()[1:]))
	return est.value(tail)
}

// TestHashSelectEmptyBodyValueRestCountsOutputBufferInBindCharge is the regression
// for the Codex thread on PR #808: a block-driven hash transform holds its
// preallocated output map and sorted-key scratch as Go locals while the block binds a
// rest-collecting destructure parameter, so those buffers must be folded into the
// bind-charge baseline (through reserveLoopScratch before the runner is built). With
// the buggy ordering the runner snapshotted the bind charge before the output map and
// scratch were reserved, so it charged the fresh tail copy against a baseline that
// omitted them. For a many-key receiver carrying one large value, the output map and
// scratch are non-trivial, so a quota that admits receiver+buffers (the walk
// preflight) and receiver+tail (the buggy bind charge) SEPARATELY but not the real
// peak receiver+buffers+tail would let the empty body bind the huge tail and escape.
// Reserving the buffers before the runner folds them into the baseline so the charge
// rejects the combined peak. select is the named subject; reject and transform_values
// share the same runner pattern.
func TestHashSelectEmptyBodyValueRestCountsOutputBufferInBindCharge(t *testing.T) {
	t.Parallel()

	// Many keys so the output map and sorted-key scratch are non-trivial, with exactly
	// one entry whose value is large enough that its rest copy dwarfs the buffers; the
	// rest carry tiny values that bind tails the quota easily admits.
	const entryCount = 256
	const bigValueLen = 200_000
	bigKey := "big"
	bigValue := arrayValue(bigValueLen)
	entries := make(map[string]Value, entryCount)
	entries[bigKey] = bigValue
	for i := range entryCount - 1 {
		entries["k"+strconv.Itoa(i)] = arrayValue(2)
	}
	receiver := NewHash(entries)
	block := emptyValueRestTransformBlock()
	if blockWantsCollapsedPair(valueBlock(block)) {
		t.Fatal("test setup expects a two-parameter block that yields key and value separately")
	}
	if !blockBindsRest(valueBlock(block)) {
		t.Fatal("test setup expects a block whose value parameter collects a rest")
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	// The transform reserves its whole output map plus the sorted-key scratch through
	// reserveLoopScratch before the runner snapshots the bind charge.
	bufferCharge := hashTransformBufferBytes(len(entries), sortedKeyBufferBytes(len(entries)))
	tailCharge := valueRestTailChargeBytes(bigKey, bigValue)

	// Sanity: the output map and scratch are genuinely non-trivial, so the buggy
	// ordering's omission of them -- not an incidentally tight quota -- is what would
	// let the walk escape.
	if bufferCharge <= 0 {
		t.Fatalf("test setup expects a non-trivial output buffer charge, got %d bytes", bufferCharge)
	}

	// A quota one byte below the real peak (roots+buffers+tail). The correct charge,
	// which now folds the buffers into the baseline, rejects this peak.
	quota := roots + bufferCharge + tailCharge - 1

	// The buggy bind charge would have measured only roots+tail, which must fit the
	// quota, proving the output buffers (not the tail or an over-tight quota) are the
	// deciding term.
	if roots+tailCharge > quota {
		t.Fatalf("test setup expects roots+tail (%d) to fit the quota (%d) so the output buffers are the deciding term", roots+tailCharge, quota)
	}
	// The walk preflight sees roots+buffers (the tail is not bound until the block is
	// called), which must also fit so the preflight does not pre-empt the bind charge.
	if roots+bufferCharge > quota {
		t.Fatalf("test setup expects roots+buffers (%d) to fit the quota (%d) so the preflight does not pre-empt the bind charge", roots+bufferCharge, quota)
	}

	for _, name := range []string{"select", "reject"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
			_, err := callHashMember(t, exec, receiver, name, nil, block)
			requireErrorIs(t, err, errMemoryQuotaExceeded)
		})
	}
}

// emptyValueArrayRestBlock builds the single-parameter |(head, *tail)| block a
// value-yielding transform (transform_values) yields each value to, destructuring
// that value array into a nested rest with an EMPTY body. The lone destructure
// parameter binds the whole yielded value and copies its tail into a fresh,
// source-sized backing; the empty body means only the per-call bind charge can
// observe that copy.
func emptyValueArrayRestBlock() Value {
	pos := Position{Line: 1, Column: 1}
	valueTarget := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "head", Position: pos}},
			{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
		},
	}
	return NewBlock([]Param{{Kind: ParamNormal, Target: valueTarget}}, nil, newEnv(nil))
}

// TestHashTransformValuesEmptyBodyValueRestCountsOutputBufferInBindCharge mirrors the
// select/reject regression for transform_values, which yields each VALUE (a single
// argument) rather than the (key, value) pair. A lone destructure-rest parameter
// |(head, *tail)| copies the value's tail into a fresh backing while the transform
// holds its preallocated output map and sorted-key scratch as Go locals. Those
// buffers must be in the bind-charge baseline (reserved through reserveLoopScratch
// before the runner) or the empty body binds the huge tail against a baseline that
// omits them and escapes a quota that fits receiver+buffers and receiver+tail apart.
func TestHashTransformValuesEmptyBodyValueRestCountsOutputBufferInBindCharge(t *testing.T) {
	t.Parallel()

	const entryCount = 256
	const bigValueLen = 200_000
	bigKey := "big"
	bigValue := arrayValue(bigValueLen)
	entries := make(map[string]Value, entryCount)
	entries[bigKey] = bigValue
	for i := range entryCount - 1 {
		entries["k"+strconv.Itoa(i)] = arrayValue(2)
	}
	receiver := NewHash(entries)
	block := emptyValueArrayRestBlock()
	if !blockBindsRest(valueBlock(block)) {
		t.Fatal("test setup expects a block whose lone parameter collects a rest")
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	bufferCharge := hashTransformBufferBytes(len(entries), sortedKeyBufferBytes(len(entries)))
	// transform_values yields the value array directly, so the lone destructure binds
	// it and the per-call estimator is seeded with that single value argument before
	// the fresh tail is charged.
	est := newMemoryEstimator()
	est.value(bigValue)
	tailCharge := est.value(NewArray(slicesClone(bigValue.Array()[1:])))

	quota := roots + bufferCharge + tailCharge - 1
	if roots+tailCharge > quota {
		t.Fatalf("test setup expects roots+tail (%d) to fit the quota (%d) so the output buffers are the deciding term", roots+tailCharge, quota)
	}
	if roots+bufferCharge > quota {
		t.Fatalf("test setup expects roots+buffers (%d) to fit the quota (%d) so the preflight does not pre-empt the bind charge", roots+bufferCharge, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "transform_values", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashSelectEmptyBodyValueRestFitsQuota pins the other side: a quota generously
// above the receiver, the output buffers, and the full tail must NOT over-reject a
// rest-collecting transform block. It admits the empty-body walk, proving the buffer
// reservation bounds only the live buffers plus tail rather than rejecting every
// rest-collecting block over a large-valued hash.
func TestHashSelectEmptyBodyValueRestFitsQuota(t *testing.T) {
	t.Parallel()

	const entryCount = 64
	const bigValueLen = 50_000
	bigKey := "big"
	bigValue := arrayValue(bigValueLen)
	entries := make(map[string]Value, entryCount)
	entries[bigKey] = bigValue
	for i := range entryCount - 1 {
		entries["k"+strconv.Itoa(i)] = arrayValue(2)
	}
	receiver := NewHash(entries)
	block := emptyValueRestTransformBlock()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	bufferCharge := hashTransformBufferBytes(len(entries), sortedKeyBufferBytes(len(entries)))
	tailCharge := valueRestTailChargeBytes(bigKey, bigValue)
	quota := roots + bufferCharge + tailCharge + 64*1024

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callHashMember(t, exec, receiver, "select", nil, block)
	if err != nil {
		t.Fatalf("select over a rest-collecting block whose buffers and tail fit the quota = %v, want success", err)
	}
	// The empty body returns nil (falsy), so select keeps no entries; the point is the
	// walk completes within the quota rather than over-rejecting.
	if got.Kind() != KindHash {
		t.Fatalf("select returned %v, want a hash", got.Kind())
	}
}
