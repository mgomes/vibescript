package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestArrayMapReservesOutputBeforeBlockCalls(t *testing.T) {
	t.Parallel()

	receiver := largeIntArray(4000)
	block := emptyBlockValue()
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	receiverBytes := probe.estimateMemoryUsage(receiver, block)
	outputSlots := arraySlotBackingBytes(len(receiver.Array()))
	quota := receiverBytes + outputSlots/2
	if quota <= receiverBytes || quota >= receiverBytes+outputSlots {
		t.Fatalf("quota %d must fit receiver/block %d and reject receiver+output slots %d", quota, receiverBytes, receiverBytes+outputSlots)
	}

	fitsCallRoots := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	if err := fitsCallRoots.checkCallMemoryRoots(receiver, nil, nil, block); err != nil {
		t.Fatalf("receiver and block should fit under quota %d: %v", quota, err)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, exec, receiver, "map", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps != 0 {
		t.Fatalf("map stepped %d times before rejecting output backing; want 0", exec.steps)
	}
}

func TestArrayMapReservesRetainedPayloadBeforeLaterBlockCalls(t *testing.T) {
	t.Parallel()

	const retainedPayloadBytes = 64 * 1024
	receiver := largeIntArray(2)
	retainedPayload := NewString(strings.Repeat("x", retainedPayloadBytes))
	expectedRetained := newMemoryEstimator().valuePayload(retainedPayload)
	expectedBacking := arraySlotBackingBytes(len(receiver.Array()))
	expectedReserved := expectedBacking + expectedRetained
	calls := 0
	block := arrayMapRetainedPayloadProbeBlock(retainedPayload.String(), expectedReserved, &calls)

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	got, err := callArrayMember(t, exec, receiver, "map", nil, block)
	if err != nil {
		t.Fatalf("array.map retained payload reservation error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("array.map block calls = %d, want 2", calls)
	}
	compareArrays(t, got, []Value{retainedPayload, retainedPayload})
	if exec.reservedScratchBytes != 0 {
		t.Fatalf("array.map leaked %d scratch bytes after success", exec.reservedScratchBytes)
	}
}

func TestArraySelectReservesOutputBeforeBlockCalls(t *testing.T) {
	t.Parallel()

	receiver := largeIntArray(4000)
	block := emptyBlockValue()
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	receiverBytes := probe.estimateMemoryUsage(receiver, block)
	outputSlots := arraySlotBackingBytes(len(receiver.Array()))
	quota := receiverBytes + outputSlots/2
	if quota <= receiverBytes || quota >= receiverBytes+outputSlots {
		t.Fatalf("quota %d must fit receiver/block %d and reject receiver+output slots %d", quota, receiverBytes, receiverBytes+outputSlots)
	}

	fitsCallRoots := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	if err := fitsCallRoots.checkCallMemoryRoots(receiver, nil, nil, block); err != nil {
		t.Fatalf("receiver and block should fit under quota %d: %v", quota, err)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, exec, receiver, "select", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps != 0 {
		t.Fatalf("select stepped %d times before rejecting output backing; want 0", exec.steps)
	}
}

func TestArrayBlockFiltersReserveEmptyResultBackingBeforeBlockCalls(t *testing.T) {
	t.Parallel()

	receiver := largeIntArray(4000)
	tests := []struct {
		method string
		block  Value
	}{
		{method: "delete_if", block: constantBoolBlockValue(true)},
		{method: "keep_if", block: constantBoolBlockValue(false)},
	}

	for _, tc := range tests {
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()

			initialCap := boundedFilterCap(len(receiver.Array()))
			emptyBacking := arraySlotBackingBytes(initialCap)
			probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
			base := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, tc.block)
			quota := base + emptyBacking - 1
			if quota <= base {
				t.Fatalf("quota %d must fit call roots %d and reject empty result backing %d", quota, base, emptyBacking)
			}

			fitsCallRoots := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
			if err := fitsCallRoots.checkCallMemoryRoots(receiver, nil, nil, tc.block); err != nil {
				t.Fatalf("array.%s call roots should fit under quota %d: %v", tc.method, quota, err)
			}

			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
			_, err := callArrayMember(t, exec, receiver, tc.method, nil, tc.block)
			requireErrorIs(t, err, errMemoryQuotaExceeded)
			if exec.steps != 0 {
				t.Fatalf("array.%s stepped %d times before rejecting empty result backing; want 0", tc.method, exec.steps)
			}
		})
	}
}

func TestArrayUniqBlockReservesOutputBeforeBlockCalls(t *testing.T) {
	t.Parallel()

	receiver := largeIntArray(4000)
	block := emptyBlockValue()
	initialCap := boundedSetCap(len(receiver.Array()))
	outputSlots := arraySlotBackingBytes(initialCap)
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	base := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	quota := base + outputSlots - 1
	if quota <= base {
		t.Fatalf("quota %d must fit call roots %d and reject uniq result backing %d", quota, base, outputSlots)
	}

	fitsCallRoots := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	if err := fitsCallRoots.checkCallMemoryRoots(receiver, nil, nil, block); err != nil {
		t.Fatalf("array.uniq call roots should fit under quota %d: %v", quota, err)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, exec, receiver, "uniq", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps != 0 {
		t.Fatalf("array.uniq stepped %d times before rejecting output backing; want 0", exec.steps)
	}
}

func TestArraySortByReservesDecoratedBufferBeforeBlockCalls(t *testing.T) {
	t.Parallel()

	receiver := largeIntArray(4000)
	block := keyIdentityBlock()
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	decorated := arraySortByDecoratedBufferBytes(len(receiver.Array()))
	quota := roots + decorated/2
	if quota <= roots || quota >= roots+decorated {
		t.Fatalf("quota %d must fit call roots %d and reject decorated buffer %d", quota, roots, roots+decorated)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, exec, receiver, "sort_by", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps != 0 {
		t.Fatalf("sort_by stepped %d times before rejecting decorated buffer; want 0", exec.steps)
	}
	if exec.reservedScratchBytes != 0 {
		t.Fatalf("sort_by leaked %d scratch bytes after rejection", exec.reservedScratchBytes)
	}
}

func TestArraySortByReservesRetainedKeysBeforeLaterBlockCalls(t *testing.T) {
	t.Parallel()

	const retainedPayloadBytes = 64 * 1024
	receiver := largeIntArray(2)
	retainedPayload := NewString(strings.Repeat("x", retainedPayloadBytes))
	expectedRetained := newMemoryEstimator().valuePayload(retainedPayload)
	expectedDecorated := arraySortByDecoratedBufferBytes(len(receiver.Array()))
	expectedReserved := expectedDecorated + expectedRetained
	calls := 0
	block := arraySortByRetainedKeyProbeBlock(retainedPayload.String(), expectedReserved, &calls)

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	got, err := callArrayMember(t, exec, receiver, "sort_by", nil, block)
	if err != nil {
		t.Fatalf("array.sort_by retained key reservation error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("array.sort_by block calls = %d, want 2", calls)
	}
	compareArrays(t, got, receiver.Array())
	if exec.reservedScratchBytes != 0 {
		t.Fatalf("array.sort_by leaked %d scratch bytes after success", exec.reservedScratchBytes)
	}
}

func TestArraySortByIncludesRetainedKeysWhenReservingOutput(t *testing.T) {
	t.Parallel()

	const keyPayloadBytes = 64 * 1024
	receiver := largeIntArray(4000)
	block := freshStringBlockValue(keyPayloadBytes)
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	decorated := arraySortByDecoratedBufferBytes(len(receiver.Array()))
	keyPayload := newMemoryEstimator().valuePayload(NewString(strings.Repeat("x", keyPayloadBytes)))
	outputSlots := arraySlotBackingBytes(len(receiver.Array()))
	quota := roots + decorated + keyPayload + outputSlots/2
	if quota <= roots+decorated+keyPayload || quota >= roots+decorated+keyPayload+outputSlots {
		t.Fatalf("quota %d must fit roots %d, decorated buffer %d, and key payload %d while rejecting output slots %d", quota, roots, decorated, keyPayload, outputSlots)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, exec, receiver, "sort_by", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps == 0 {
		t.Fatalf("sort_by rejected before retaining sort keys; want at least one step")
	}
	if exec.reservedScratchBytes != 0 {
		t.Fatalf("sort_by leaked %d scratch bytes after rejection", exec.reservedScratchBytes)
	}
}

func TestArrayGroupByReservesGroupedSlicesDuringBlockCalls(t *testing.T) {
	t.Parallel()

	receiver := largeIntArray(4000)
	block := constantSymbolBlockValue("all")
	quota := groupedSingleBucketQuota(t, receiver, block)

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, exec, receiver, "group_by", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps == 0 {
		t.Fatalf("group_by rejected before exercising grouped slice growth; want at least one step")
	}
	if exec.reservedScratchBytes != 0 {
		t.Fatalf("group_by leaked %d scratch bytes after rejection", exec.reservedScratchBytes)
	}
}

func TestArrayGroupByStableReservesGroupedSlicesDuringBlockCalls(t *testing.T) {
	t.Parallel()

	receiver := largeIntArray(4000)
	block := constantSymbolBlockValue("all")
	quota := groupedSingleBucketQuota(t, receiver, block)

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, exec, receiver, "group_by_stable", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps == 0 {
		t.Fatalf("group_by_stable rejected before exercising grouped slice growth; want at least one step")
	}
	if exec.reservedScratchBytes != 0 {
		t.Fatalf("group_by_stable leaked %d scratch bytes after rejection", exec.reservedScratchBytes)
	}
}

func TestArrayChunkByBlockPreflightsGroupBacking(t *testing.T) {
	t.Parallel()

	receiver := largeIntArray(4000)
	block := constantSymbolBlockValue("all")
	initialCap := boundedFilterCap(len(receiver.Array()))
	outerBacking := arraySlotBackingBytes(initialCap)
	pairBacking := arraySlotBackingBytes(2)
	groupBacking := arraySlotBackingBytes(len(receiver.Array()))

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	base := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	quota := base + outerBacking + pairBacking + groupBacking/2
	if quota <= base+outerBacking+pairBacking || quota >= base+outerBacking+pairBacking+groupBacking {
		t.Fatalf("quota %d must fit roots %d, outer backing %d, and pair backing %d while rejecting group backing %d",
			quota, base, outerBacking, pairBacking, groupBacking)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, exec, receiver, "chunk", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps == 0 {
		t.Fatalf("chunk rejected before exercising block grouping; want at least one step")
	}
}

func TestArrayChunkByBlockReservesRetainedKeyBeforeLaterBlockCalls(t *testing.T) {
	t.Parallel()

	const retainedPayloadBytes = 64 * 1024
	receiver := largeIntArray(2)
	retainedPayload := NewString(strings.Repeat("x", retainedPayloadBytes))
	expectedRetained := newMemoryEstimator().valuePayload(retainedPayload)
	calls := 0
	block := arrayChunkRetainedKeyProbeBlock(retainedPayload.String(), expectedRetained, &calls)

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	got, err := callArrayMember(t, exec, receiver, "chunk", nil, block)
	if err != nil {
		t.Fatalf("array.chunk retained key reservation error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("array.chunk block calls = %d, want 2", calls)
	}
	compareArrays(t, got, []Value{NewArray([]Value{retainedPayload, receiver})})
	if exec.reservedScratchBytes != 0 {
		t.Fatalf("array.chunk leaked %d scratch bytes after success", exec.reservedScratchBytes)
	}
}

func TestArrayAdjacentSlicesPreflightSegmentBacking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		member string
		block  Value
	}{
		{name: "slice_when", member: "slice_when", block: constantBoolBlockValue(false)},
		{name: "chunk_while", member: "chunk_while", block: constantBoolBlockValue(true)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			receiver := largeIntArray(4000)
			initialCap := boundedFilterCap(len(receiver.Array()))
			outerBacking := arraySlotBackingBytes(initialCap)
			segmentBacking := arraySlotBackingBytes(len(receiver.Array()))

			probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
			base := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, tc.block)
			quota := base + outerBacking + segmentBacking/2
			if quota <= base+outerBacking || quota >= base+outerBacking+segmentBacking {
				t.Fatalf("quota %d must fit roots %d and outer backing %d while rejecting segment backing %d",
					quota, base, outerBacking, segmentBacking)
			}

			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
			_, err := callArrayMember(t, exec, receiver, tc.member, nil, tc.block)
			requireErrorIs(t, err, errMemoryQuotaExceeded)
			if exec.steps == 0 {
				t.Fatalf("%s rejected before exercising adjacent scan; want at least one step", tc.member)
			}
		})
	}
}

func TestArrayProductPreflightsTupleRowBacking(t *testing.T) {
	t.Parallel()

	const dims = 1024
	receiver := NewArray([]Value{NewInt(0)})
	args := make([]Value, dims-1)
	for i := range args {
		args[i] = NewArray([]Value{NewInt(int64(i + 1))})
	}

	scratch := arrayIntScratchBytes(dims)
	outerBacking := arraySlotBackingBytes(1)
	rowBacking := arrayTupleRowBackingBytes(1, dims)

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	base := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, args, nil, NewNil())
	quota := base + scratch + outerBacking + rowBacking/2
	if quota <= base+scratch+outerBacking || quota >= base+scratch+outerBacking+rowBacking {
		t.Fatalf("quota %d must fit roots %d, scratch %d, and outer backing %d while rejecting product row backing %d",
			quota, base, scratch, outerBacking, rowBacking)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, exec, receiver, "product", args, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps != 0 {
		t.Fatalf("product advanced to %d steps before rejecting tuple row backing; want 0", exec.steps)
	}
}

func TestArrayTallyReservesCountMapDuringBlockCalls(t *testing.T) {
	t.Parallel()

	receiver := largeSymbolArray(2000)
	block := keyIdentityBlock()
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	initialCapacity, _, err := arrayTallyInitialCapacity(probe, receiver.Array(), true)
	if err != nil {
		t.Fatalf("arrayTallyInitialCapacity(block) error = %v", err)
	}
	initialScratch := hashAggregationMapScratchBytes(1, initialCapacity)
	initialScratch = saturatingAdd(initialScratch, arrayTallyBucketSliceScratchBytes(initialCapacity))
	quota := roots + initialScratch + 1024
	if quota <= roots+initialScratch {
		t.Fatalf("quota %d must fit roots %d and initial tally scratch %d", quota, roots, initialScratch)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err = callArrayMember(t, exec, receiver, "tally", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps == 0 {
		t.Fatalf("tally rejected before exercising count-map growth; want at least one step")
	}
	if exec.reservedScratchBytes != 0 {
		t.Fatalf("tally leaked %d scratch bytes after rejection", exec.reservedScratchBytes)
	}
}

func TestArrayTallyDoesNotRechargeReceiverOwnedKeyPayload(t *testing.T) {
	t.Parallel()

	const count = 32
	const payloadBytes = 4096

	values := make([]Value, count)
	for i := range count {
		values[i] = NewString(fmt.Sprintf("%04d-%s", i, strings.Repeat("x", payloadBytes)))
	}
	receiver := NewArray(values)
	block := NewNil()
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	initialCapacity, _, err := arrayTallyInitialCapacity(probe, receiver.Array(), false)
	if err != nil {
		t.Fatalf("arrayTallyInitialCapacity(blockless) error = %v", err)
	}
	initialScratch := hashAggregationMapScratchBytes(1, initialCapacity)
	initialScratch = saturatingAdd(initialScratch, arrayTallyBucketSliceScratchBytes(initialCapacity))
	resultScratch := hashResultBytes(count)
	quota := roots + initialScratch + resultScratch + 1024

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callArrayMember(t, exec, receiver, "tally", nil, block)
	if err != nil {
		t.Fatalf("array.tally with receiver-owned key payloads under quota %d: %v", quota, err)
	}
	if len(got.Hash()) != count {
		t.Fatalf("array.tally result entries = %d, want %d", len(got.Hash()), count)
	}
	if exec.reservedScratchBytes != 0 {
		t.Fatalf("tally leaked %d scratch bytes after success", exec.reservedScratchBytes)
	}
}

func TestArrayJoinChargesReceiverBeforeBuilderGrowth(t *testing.T) {
	t.Parallel()

	const parts = 8
	chunk := strings.Repeat("x", 1000)
	elements := make([]Value, parts)
	for i := range elements {
		elements[i] = NewString(chunk)
	}
	receiver := NewArray(elements)
	sep := NewString("")

	finalString := NewString(strings.Repeat("x", parts*len(chunk)))
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	receiverBytes := probe.estimateMemoryUsage(receiver, sep)
	finalBytes := probe.estimateMemoryUsage(finalString)
	larger := max(receiverBytes, finalBytes)
	quota := larger + (receiverBytes+finalBytes-larger)/2
	if quota <= larger || quota >= receiverBytes+finalBytes {
		t.Fatalf("quota %d must fit larger single footprint %d and reject combined footprint %d", quota, larger, receiverBytes+finalBytes)
	}

	fitsCallRoots := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	if err := fitsCallRoots.checkCallMemoryRoots(receiver, []Value{sep}, nil, NewNil()); err != nil {
		t.Fatalf("receiver and separator should fit under quota %d: %v", quota, err)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, exec, receiver, "join", []Value{sep}, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

func TestArraySortParticipatesInStepQuota(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 40, MemoryQuotaBytes: 64 << 20}, `
def run(values)
  values.sort.size
end
`)

	requireCallRuntimeErrorType(t, script, "run", []Value{largeIntArray(1000)}, CallOptions{}, runtimeErrorTypeLimit)
}

func TestArrayFlattenChargesResultDuringBuild(t *testing.T) {
	t.Parallel()

	const groups = 8
	const perGroup = 500
	const leaves = groups * perGroup
	nested := make([]Value, groups)
	want := make([]Value, 0, leaves)
	for i := range nested {
		inner := make([]Value, perGroup)
		for j := range inner {
			inner[j] = NewInt(int64(i*perGroup + j))
			want = append(want, inner[j])
		}
		nested[i] = NewArray(inner)
	}
	receiver := NewArray(nested)

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	base := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, NewNil())

	t.Run("under_quota", func(t *testing.T) {
		t.Parallel()

		// The growth checks charge the doubled backing before each append grows
		// it, so a successful build never projects more than twice the largest
		// capacity the append schedule reaches; four times the leaf count bounds
		// that projection with room for the schedule's overshoot past the final
		// length.
		quota := base + arraySlotBackingBytes(4*leaves) + 4096
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		got, err := callArrayMember(t, exec, receiver, "flatten", nil, NewNil())
		if err != nil {
			t.Fatalf("array.flatten under quota %d: %v", quota, err)
		}
		compareArrays(t, got, want)
	})

	t.Run("over_quota", func(t *testing.T) {
		t.Parallel()

		quota := base + arraySlotBackingBytes(leaves/2)
		fitsCallRoots := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		if err := fitsCallRoots.checkCallMemoryRoots(receiver, nil, nil, NewNil()); err != nil {
			t.Fatalf("receiver should fit under quota %d: %v", quota, err)
		}

		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		_, err := callArrayMember(t, exec, receiver, "flatten", nil, NewNil())
		requireErrorIs(t, err, errMemoryQuotaExceeded)
		if exec.steps >= leaves {
			t.Fatalf("array.flatten examined %d elements before rejecting; want rejection before the %d-leaf result materializes", exec.steps, leaves)
		}
	})
}

func TestArrayWindowPreflightsResultBacking(t *testing.T) {
	t.Parallel()

	const elements = 4000
	const windowSize = 3
	const windowCount = elements - windowSize + 1
	receiver := largeIntArray(elements)
	size := NewInt(windowSize)

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	base := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, []Value{size}, nil, NewNil())

	t.Run("under_quota", func(t *testing.T) {
		t.Parallel()

		arr := receiver.Array()
		est := newMemoryEstimator()
		payload := 0
		want := make([]Value, windowCount)
		for i := range want {
			row := make([]Value, windowSize)
			copy(row, arr[i:i+windowSize])
			want[i] = NewArray(row)
			payload += est.valuePayload(want[i])
		}
		// Mirror the build's peak projection: the outer slot reservation, one
		// in-flight window backing, and every retained window payload.
		quota := base + arraySlotBackingBytes(windowCount) + arraySlotBackingBytes(windowSize) + payload + 4096
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		got, err := callArrayMember(t, exec, receiver, "window", []Value{size}, NewNil())
		if err != nil {
			t.Fatalf("array.window under quota %d: %v", quota, err)
		}
		compareArrays(t, got, want)
	})

	t.Run("over_quota", func(t *testing.T) {
		t.Parallel()

		quota := base + arraySlotBackingBytes(windowCount)/2
		fitsCallRoots := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		if err := fitsCallRoots.checkCallMemoryRoots(receiver, []Value{size}, nil, NewNil()); err != nil {
			t.Fatalf("receiver and size should fit under quota %d: %v", quota, err)
		}

		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		_, err := callArrayMember(t, exec, receiver, "window", []Value{size}, NewNil())
		requireErrorIs(t, err, errMemoryQuotaExceeded)
		if exec.steps != 0 {
			t.Fatalf("array.window stepped %d times before rejecting the outer backing; want 0", exec.steps)
		}
	})
}

func TestArrayJoinQuotaCoversRenderedPayload(t *testing.T) {
	t.Parallel()

	const parts = 8
	chunk := strings.Repeat("x", 1000)
	elements := make([]Value, 0, parts+2)
	wantParts := make([]string, 0, parts+2)
	for range parts {
		elements = append(elements, NewString(chunk))
		wantParts = append(wantParts, chunk)
	}
	// Mixed scalar types pin the rendered-payload bound: the int renders as its
	// decimal form and nil contributes an empty segment, exactly as the built
	// string will contain them.
	elements = append(elements, NewInt(12345), NewNil())
	wantParts = append(wantParts, "12345", "")
	receiver := NewArray(elements)
	sep := NewString("-")
	want := strings.Join(wantParts, "-")

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	base := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, []Value{sep}, nil, NewNil())
	finalBytes := probe.estimateMemoryUsage(NewString(want))

	t.Run("under_quota", func(t *testing.T) {
		t.Parallel()

		quota := base + finalBytes + 16384
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		got, err := callArrayMember(t, exec, receiver, "join", []Value{sep}, NewNil())
		if err != nil {
			t.Fatalf("array.join under quota %d: %v", quota, err)
		}
		if got.Kind() != KindString || got.String() != want {
			t.Fatalf("array.join result mismatch: got %d bytes, want %d bytes", len(got.String()), len(want))
		}
	})

	t.Run("over_quota", func(t *testing.T) {
		t.Parallel()

		quota := base + finalBytes/2
		fitsCallRoots := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		if err := fitsCallRoots.checkCallMemoryRoots(receiver, []Value{sep}, nil, NewNil()); err != nil {
			t.Fatalf("receiver and separator should fit under quota %d: %v", quota, err)
		}

		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		_, err := callArrayMember(t, exec, receiver, "join", []Value{sep}, NewNil())
		requireErrorIs(t, err, errMemoryQuotaExceeded)
	})
}

// TestArrayChunkBlocklessChargesResultDuringBuild pins the blockless
// Array#chunk(n) quota thresholds: the outer slot reservation rejects before
// the loop starts, a quota covering the whole build succeeds, and a quota that
// admits the reservation but not every chunk's payload is rejected mid-build
// by the accumulator. The mid-build case is the guard for the
// accumulator-metered section: the loop skips the periodic reachable-graph
// walk, so the accumulator's own charges must remain the binding constraint at
// exactly the same thresholds.
func TestArrayChunkBlocklessChargesResultDuringBuild(t *testing.T) {
	t.Parallel()

	const elements = 4096
	const size = 16
	const chunkCount = elements / size
	receiver := largeIntArray(elements)
	sizeArg := NewInt(size)

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	base := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, []Value{sizeArg}, nil, NewNil())

	arr := receiver.Array()
	est := newMemoryEstimator()
	payload := 0
	want := make([]Value, 0, chunkCount)
	for i := 0; i < elements; i += size {
		part := make([]Value, size)
		copy(part, arr[i:i+size])
		want = append(want, NewArray(part))
		payload += est.valuePayload(want[len(want)-1])
	}

	t.Run("under_quota", func(t *testing.T) {
		t.Parallel()

		// Mirror the build's peak projection: the outer slot reservation, one
		// in-flight chunk backing, and every retained chunk payload.
		quota := base + arraySlotBackingBytes(chunkCount) + arraySlotBackingBytes(size) + payload + 4096
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		got, err := callArrayMember(t, exec, receiver, "chunk", []Value{sizeArg}, NewNil())
		if err != nil {
			t.Fatalf("array.chunk under quota %d: %v", quota, err)
		}
		compareArrays(t, got, want)
	})

	t.Run("over_quota_preflight", func(t *testing.T) {
		t.Parallel()

		quota := base + arraySlotBackingBytes(chunkCount)/2
		fitsCallRoots := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		if err := fitsCallRoots.checkCallMemoryRoots(receiver, []Value{sizeArg}, nil, NewNil()); err != nil {
			t.Fatalf("receiver and size should fit under quota %d: %v", quota, err)
		}

		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		_, err := callArrayMember(t, exec, receiver, "chunk", []Value{sizeArg}, NewNil())
		requireErrorIs(t, err, errMemoryQuotaExceeded)
		if exec.steps != 0 {
			t.Fatalf("array.chunk stepped %d times before rejecting the outer backing; want 0", exec.steps)
		}
	})

	t.Run("over_quota_midbuild", func(t *testing.T) {
		t.Parallel()

		quota := base + arraySlotBackingBytes(chunkCount) + payload/2
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		_, err := callArrayMember(t, exec, receiver, "chunk", []Value{sizeArg}, NewNil())
		requireErrorIs(t, err, errMemoryQuotaExceeded)
		if exec.steps == 0 || exec.steps >= chunkCount {
			t.Fatalf("array.chunk rejected after %d steps; want a mid-build rejection in (0, %d)", exec.steps, chunkCount)
		}
	})
}

// TestArrayReverseCopyPreflightsResultBacking pins Array#reverse's quota
// thresholds around its up-front slot reservation. Every retained element
// aliases the receiver already charged in the accumulator baseline, so the
// reservation is the whole threshold: a quota that covers it succeeds and one
// that does not is rejected before the loop allocates or steps.
func TestArrayReverseCopyPreflightsResultBacking(t *testing.T) {
	t.Parallel()

	const elements = 4000
	receiver := largeIntArray(elements)

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	base := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, NewNil())

	t.Run("under_quota", func(t *testing.T) {
		t.Parallel()

		quota := base + arraySlotBackingBytes(elements) + 4096
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		got, err := callArrayMember(t, exec, receiver, "reverse", nil, NewNil())
		if err != nil {
			t.Fatalf("array.reverse under quota %d: %v", quota, err)
		}
		arr := receiver.Array()
		want := make([]Value, elements)
		for i, item := range arr {
			want[elements-1-i] = item
		}
		compareArrays(t, got, want)
	})

	t.Run("over_quota", func(t *testing.T) {
		t.Parallel()

		quota := base + arraySlotBackingBytes(elements)/2
		fitsCallRoots := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		if err := fitsCallRoots.checkCallMemoryRoots(receiver, nil, nil, NewNil()); err != nil {
			t.Fatalf("receiver should fit under quota %d: %v", quota, err)
		}

		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		_, err := callArrayMember(t, exec, receiver, "reverse", nil, NewNil())
		requireErrorIs(t, err, errMemoryQuotaExceeded)
		if exec.steps != 0 {
			t.Fatalf("array.reverse stepped %d times before rejecting the backing; want 0", exec.steps)
		}
	})
}

func constantSymbolBlockValue(name string) Value {
	pos := Position{Line: 1, Column: 1}
	body := []Statement{&ExprStmt{Position: pos, Expr: &SymbolLiteral{Name: name, Position: pos}}}
	return NewBlock(nil, body, newEnv(nil))
}

func constantBoolBlockValue(value bool) Value {
	pos := Position{Line: 1, Column: 1}
	body := []Statement{&ExprStmt{Position: pos, Expr: &BoolLiteral{Value: value, Position: pos}}}
	return NewBlock(nil, body, newEnv(nil))
}

func arrayMapRetainedPayloadProbeBlock(payload string, expectedReservedBeforeSecondCall int, calls *int) Value {
	pos := Position{Line: 1, Column: 1}
	probe := NewBuiltin("test.array_map_payload_probe", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		*calls++
		if *calls == 2 && exec.reservedScratchBytes < expectedReservedBeforeSecondCall {
			return NewNil(), fmt.Errorf("reserved scratch before second map block call = %d, want at least %d", exec.reservedScratchBytes, expectedReservedBeforeSecondCall)
		}
		return NewString(payload), nil
	})
	env := newEnv(nil)
	env.Define("__probe__", probe)
	body := []Statement{&ExprStmt{Position: pos, Expr: &CallExpr{
		Position: pos,
		Callee:   &Identifier{Name: "__probe__", Position: pos},
	}}}
	return NewBlock([]Param{{Kind: ParamNormal, Name: "item"}}, body, env)
}

func arraySortByRetainedKeyProbeBlock(payload string, expectedReservedBeforeSecondCall int, calls *int) Value {
	pos := Position{Line: 1, Column: 1}
	probe := NewBuiltin("test.array_sort_by_payload_probe", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		*calls++
		if *calls == 2 && exec.reservedScratchBytes < expectedReservedBeforeSecondCall {
			return NewNil(), fmt.Errorf("reserved scratch before second sort_by block call = %d, want at least %d", exec.reservedScratchBytes, expectedReservedBeforeSecondCall)
		}
		return NewString(payload), nil
	})
	env := newEnv(nil)
	env.Define("__probe__", probe)
	body := []Statement{&ExprStmt{Position: pos, Expr: &CallExpr{
		Position: pos,
		Callee:   &Identifier{Name: "__probe__", Position: pos},
	}}}
	return NewBlock([]Param{{Kind: ParamNormal, Name: "item"}}, body, env)
}

func arrayChunkRetainedKeyProbeBlock(payload string, expectedReservedBeforeSecondCall int, calls *int) Value {
	pos := Position{Line: 1, Column: 1}
	probe := NewBuiltin("test.array_chunk_key_probe", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		*calls++
		if *calls == 2 && exec.reservedScratchBytes < expectedReservedBeforeSecondCall {
			return NewNil(), fmt.Errorf("reserved scratch before second chunk block call = %d, want at least %d", exec.reservedScratchBytes, expectedReservedBeforeSecondCall)
		}
		return NewString(payload), nil
	})
	env := newEnv(nil)
	env.Define("__probe__", probe)
	body := []Statement{&ExprStmt{Position: pos, Expr: &CallExpr{
		Position: pos,
		Callee:   &Identifier{Name: "__probe__", Position: pos},
	}}}
	return NewBlock([]Param{{Kind: ParamNormal, Name: "item"}}, body, env)
}

func groupedSingleBucketQuota(t *testing.T, receiver, block Value) int {
	t.Helper()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	initialCapacity := arrayGroupingInitialCapacity(len(receiver.Array()))
	initialScratch := hashAggregationMapScratchBytes(1, initialCapacity)
	initialScratch = saturatingAdd(initialScratch, arrayGroupBucketSliceScratchBytes(initialCapacity))
	key := NewSymbol("all")
	// The bucket key string aliases the block result's own payload, so only
	// the key value itself is fresh scratch.
	firstKeyScratch := newMemoryEstimator().valuePayload(key)
	groupCap := 0
	for i := range len(receiver.Array()) {
		groupCap = projectedAppendCap(i, groupCap)
	}
	fullGroupScratch := valueSliceScratchBytes(groupCap)
	quota := roots + initialScratch + firstKeyScratch + fullGroupScratch/2
	if quota <= roots+initialScratch+firstKeyScratch || quota >= roots+initialScratch+firstKeyScratch+fullGroupScratch {
		t.Fatalf("quota %d must fit roots %d plus initial scratch %d and reject full group scratch %d", quota, roots, initialScratch, fullGroupScratch)
	}
	return quota
}

func largeSymbolArray(n int) Value {
	values := make([]Value, n)
	for i := range n {
		values[i] = NewSymbol("k" + strconv.Itoa(i))
	}
	return NewArray(values)
}
