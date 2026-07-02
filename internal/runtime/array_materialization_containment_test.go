package runtime

import (
	"context"
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
	initialScratch := estimatedMapBaseBytes + initialCapacity*estimatedMapEntryStructuralBytes
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

func groupedSingleBucketQuota(t *testing.T, receiver, block Value) int {
	t.Helper()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)
	initialCapacity := arrayGroupingInitialCapacity(len(receiver.Array()))
	initialScratch := estimatedMapBaseBytes + initialCapacity*estimatedMapEntryStructuralBytes
	groupCap := 0
	for i := range len(receiver.Array()) {
		groupCap = projectedAppendCap(i, groupCap)
	}
	fullGroupScratch := valueSliceScratchBytes(groupCap)
	quota := roots + initialScratch + len("all") + fullGroupScratch/2
	if quota <= roots+initialScratch || quota >= roots+initialScratch+len("all")+fullGroupScratch {
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
