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
	initialCapacity, err := arrayTallyInitialCapacity(receiver.Array(), true)
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
	initialCapacity, err := arrayTallyInitialCapacity(receiver.Array(), false)
	if err != nil {
		t.Fatalf("arrayTallyInitialCapacity(blockless) error = %v", err)
	}
	initialScratch := hashAggregationMapScratchBytes(1, initialCapacity)
	initialScratch = saturatingAdd(initialScratch, arrayTallyBucketSliceScratchBytes(initialCapacity))
	resultScratch := typedHashResultBytes(count, 0)
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

func groupedSingleBucketQuota(t *testing.T, receiver, block Value) int {
	t.Helper()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	initialCapacity := arrayGroupingInitialCapacity(len(receiver.Array()))
	initialScratch := hashAggregationMapScratchBytes(1, initialCapacity)
	initialScratch = saturatingAdd(initialScratch, arrayGroupBucketSliceScratchBytes(initialCapacity))
	key := NewSymbol("all")
	aggregationKey, err := newHashAggregationKey(key)
	if err != nil {
		t.Fatalf("newHashAggregationKey(all) error = %v", err)
	}
	firstKeyScratch := hashAggregationKeyScratchPayloadBytes(aggregationKey)
	firstKeyScratch = saturatingAdd(firstKeyScratch, newMemoryEstimator().valuePayload(key))
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
