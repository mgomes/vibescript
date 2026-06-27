package runtime

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestArrayToHash(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def symbol_keys()
      [[:a, 1], [:b, 2]].to_h
    end

    def string_keys()
      [["a", 1], ["b", 2]].to_h
    end

    def mixed_keys()
      [[:a, 1], ["b", 2]].to_h
    end

    def duplicate_keys_keep_last()
      [[:a, 1], [:a, 2], [:a, 3]].to_h
    end

    def empty_array()
      [].to_h
    end

    def block_maps_pairs()
      [:a, :b].to_h { |s| [s, 1] }
    end

    def block_overrides_existing_pairs()
      [[:ignored, 0]].to_h { |pair| [:kept, 9] }
    end
    `)

	tests := []struct {
		name string
		fn   string
		want map[string]Value
	}{
		{
			name: "symbol keys",
			fn:   "symbol_keys",
			want: map[string]Value{"a": NewInt(1), "b": NewInt(2)},
		},
		{
			name: "string keys convert through the hash key rules",
			fn:   "string_keys",
			want: map[string]Value{"a": NewInt(1), "b": NewInt(2)},
		},
		{
			name: "symbol and string keys share the same key space",
			fn:   "mixed_keys",
			want: map[string]Value{"a": NewInt(1), "b": NewInt(2)},
		},
		{
			name: "duplicate keys keep the last pair like Ruby",
			fn:   "duplicate_keys_keep_last",
			want: map[string]Value{"a": NewInt(3)},
		},
		{
			name: "empty array converts to an empty hash",
			fn:   "empty_array",
			want: map[string]Value{},
		},
		{
			name: "block maps each element to a pair",
			fn:   "block_maps_pairs",
			want: map[string]Value{"a": NewInt(1), "b": NewInt(1)},
		},
		{
			name: "block result is used instead of the element",
			fn:   "block_overrides_existing_pairs",
			want: map[string]Value{"kept": NewInt(9)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tt.fn, nil)
			if got.Kind() != KindHash {
				t.Fatalf("expected hash, got %v", got.Kind())
			}
			compareHash(t, got.Hash(), tt.want)
		})
	}
}

func TestArrayToHashRejectsMisuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name:    "non-array element",
			source:  "def run() [:a, :b].to_h end",
			wantErr: "array.to_h expects an array of two-element pairs",
		},
		{
			name:    "pair too short",
			source:  "def run() [[:a]].to_h end",
			wantErr: "array.to_h pair must have exactly two elements",
		},
		{
			name:    "pair too long",
			source:  "def run() [[:a, 1, 2]].to_h end",
			wantErr: "array.to_h pair must have exactly two elements",
		},
		{
			name:    "unsupported key type",
			source:  "def run() [[1, 2]].to_h end",
			wantErr: "array.to_h pair key must be symbol or string",
		},
		{
			name:    "block returns a non-pair",
			source:  "def run() [:a].to_h { |s| s } end",
			wantErr: "array.to_h expects an array of two-element pairs",
		},
		{
			name:    "positional argument",
			source:  "def run() [[:a, 1]].to_h(2) end",
			wantErr: "array.to_h does not take arguments",
		},
		{
			name:    "keyword argument",
			source:  "def run() [[:a, 1]].to_h(depth: 2) end",
			wantErr: "array.to_h does not take keyword arguments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.wantErr)
		})
	}
}

func TestHashToArray(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def pairs()
      { a: 1, b: 2 }.to_a
    end

    def deterministic_order()
      { c: 3, a: 1, b: 2 }.to_a
    end

    def nested_values_kept()
      { a: [1, 2], b: { c: 3 } }.to_a
    end

    def empty_hash()
      {}.to_a
    end
    `)

	tests := []struct {
		name string
		fn   string
		want []Value
	}{
		{
			name: "key-value pairs",
			fn:   "pairs",
			want: []Value{
				NewArray([]Value{NewSymbol("a"), NewInt(1)}),
				NewArray([]Value{NewSymbol("b"), NewInt(2)}),
			},
		},
		{
			name: "pairs follow deterministic sorted-key order",
			fn:   "deterministic_order",
			want: []Value{
				NewArray([]Value{NewSymbol("a"), NewInt(1)}),
				NewArray([]Value{NewSymbol("b"), NewInt(2)}),
				NewArray([]Value{NewSymbol("c"), NewInt(3)}),
			},
		},
		{
			name: "nested values are preserved as-is",
			fn:   "nested_values_kept",
			want: []Value{
				NewArray([]Value{NewSymbol("a"), NewArray([]Value{NewInt(1), NewInt(2)})}),
				NewArray([]Value{NewSymbol("b"), NewHash(map[string]Value{"c": NewInt(3)})}),
			},
		},
		{
			name: "empty hash converts to an empty array",
			fn:   "empty_hash",
			want: []Value{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tt.fn, nil)
			compareArrays(t, got, tt.want)
		})
	}
}

func TestEmptyConversionsDoNotRequireElementStep(t *testing.T) {
	t.Parallel()

	arrayExec := &Execution{ctx: context.Background(), quota: 1, steps: 1, memoryQuota: 0}
	hash, err := callArrayMember(t, arrayExec, NewArray(nil), "to_h", nil, NewNil())
	if err != nil {
		t.Fatalf("[].to_h with no remaining element steps returned error: %v", err)
	}
	if hash.Kind() != KindHash {
		t.Fatalf("[].to_h returned %v, want hash", hash.Kind())
	}
	if len(hash.Hash()) != 0 {
		t.Fatalf("[].to_h returned %d entries, want 0", len(hash.Hash()))
	}
	if arrayExec.steps != 1 {
		t.Fatalf("[].to_h consumed %d steps, want it to stay at 1", arrayExec.steps)
	}

	hashExec := &Execution{ctx: context.Background(), quota: 1, steps: 1, memoryQuota: 0}
	array, err := callHashMember(t, hashExec, NewHash(map[string]Value{}), "to_a", nil, NewNil())
	if err != nil {
		t.Fatalf("{}.to_a with no remaining element steps returned error: %v", err)
	}
	compareArrays(t, array, []Value{})
	if hashExec.steps != 1 {
		t.Fatalf("{}.to_a consumed %d steps, want it to stay at 1", hashExec.steps)
	}
}

func TestHashToArrayRejectsMisuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name:    "positional argument",
			source:  "def run() { a: 1 }.to_a(1) end",
			wantErr: "hash.to_a does not take arguments",
		},
		{
			name:    "keyword argument",
			source:  "def run() { a: 1 }.to_a(depth: 2) end",
			wantErr: "hash.to_a does not take keyword arguments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.wantErr)
		})
	}
}

// TestArrayToHashRoundTrip verifies Hash#to_a and Array#to_h are inverses for a
// hash with the same symbol/string key model, matching Ruby's round-trip.
func TestArrayToHashRoundTrip(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def round_trip()
      { a: 1, b: 2, c: 3 }.to_a.to_h
    end
    `)

	got := callFunc(t, script, "round_trip", nil)
	if got.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", got.Kind())
	}
	compareHash(t, got.Hash(), map[string]Value{"a": NewInt(1), "b": NewInt(2), "c": NewInt(3)})
}

// TestArrayToHashHonorsMemoryQuota verifies the output map projection trips the
// quota before the hash materializes, mirroring the other map-producing helpers.
func TestArrayToHashHonorsMemoryQuota(t *testing.T) {
	t.Parallel()

	// An array of distinct string-keyed pairs converts into a map roughly the size
	// of the input. Sizing the quota to admit the bound input with a slim margin
	// forces the limit to trip on the freshly built map rather than on argument
	// binding.
	const count = 4000
	pairs := make([]Value, count)
	for i := range count {
		key := NewString(string(rune('a'+i%26)) + string(rune('0'+i/26%10)) + string(rune('0'+i/260)))
		pairs[i] = NewArray([]Value{key, NewInt(int64(i))})
	}
	pairsArr := NewArray(pairs)

	inputBytes := newMemoryEstimator().value(pairsArr)
	quota := inputBytes + inputBytes/4

	cfg := Config{StepQuota: 1_000_000, MemoryQuotaBytes: quota}

	fits := compileScriptWithConfig(t, cfg, `def run(a); a.size; end`)
	if _, err := fits.Call(context.Background(), "run", []Value{pairsArr}, CallOptions{}); err != nil {
		t.Fatalf("input should fit under quota %d: %v", quota, err)
	}

	converts := compileScriptWithConfig(t, cfg, `def run(a); a.to_h; end`)
	requireCallRuntimeErrorType(t, converts, "run", []Value{pairsArr}, CallOptions{}, runtimeErrorTypeLimit)
}

// largeArrayPairReceiver builds an array of count distinct [symbol, int] pairs
// suitable for the bare form of Array#to_h, where every element is already a
// two-element pair so no block is needed.
func largeArrayPairReceiver(count int) Value {
	pairs := make([]Value, count)
	for i := range count {
		pairs[i] = NewArray([]Value{NewSymbol("k" + strconv.Itoa(i)), NewInt(int64(i))})
	}
	return NewArray(pairs)
}

// TestArrayToHashBareFormHonorsStepQuota pins the bare-form half of the P2
// finding: Array#to_h with no block (runner is nil) must charge a step per
// element so a tiny step quota stops it mid-loop. Before the fix the step charge
// lived inside the block-only branch, so the bare form converted every pair
// without touching the step quota and a huge pairs.to_h ran to completion.
func TestArrayToHashBareFormHonorsStepQuota(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 40, memoryQuota: 64 << 20}
	_, err := callArrayMember(t, exec, largeArrayPairReceiver(2_000), "to_h", nil, NewNil())
	requireErrorIs(t, err, errStepQuotaExceeded)
}

// TestArrayToHashBareFormHonorsCancellation verifies the bare form's per-element
// step check observes a canceled context, so a sandboxed pairs.to_h cannot keep
// converting after cancellation. step polls the context on its first call, so
// even a tiny array aborts.
func TestArrayToHashBareFormHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := &Execution{ctx: ctx, quota: 1 << 30, memoryQuota: 0}
	_, err := callArrayMember(t, exec, largeArrayPairReceiver(8), "to_h", nil, NewNil())
	requireErrorIs(t, err, context.Canceled)
}

// TestArrayToHashChecksBudgetBeforePreallocating pins the up-front half of the P2
// finding: with a large receiver, an already-canceled context, and no memory
// quota, Array#to_h must abort before the make reserves a map sized to the whole
// receiver. The per-element loop's first exec.step does poll the canceled context,
// but only after that full-sized map is already allocated. Asserting exec.steps
// stayed 0 distinguishes the up-front checkStepBudgetFor from the in-loop step: without
// the guard the loop runs its first step (after the make) and steps reaches 1.
func TestArrayToHashChecksBudgetBeforePreallocating(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := &Execution{ctx: ctx, quota: 1 << 30, memoryQuota: 0}
	_, err := callArrayMember(t, exec, largeArrayPairReceiver(4000), "to_h", nil, NewNil())
	requireErrorIs(t, err, context.Canceled)
	if exec.steps != 0 {
		t.Fatalf("expected to_h to abort before preallocating the output map (0 steps), but %d step(s) ran", exec.steps)
	}
}

// TestArrayToHashChecksStepQuotaBeforePreallocating is the step-quota twin: an
// already-spent step quota (steps == quota) must abort Array#to_h before the
// output map is reserved. Pre-spending the quota and asserting steps never
// advances past it pins the up-front checkStepBudgetFor; without it the in-loop step
// would increment past the quota before reporting the error, by which point the
// full-sized map has already been allocated.
func TestArrayToHashChecksStepQuotaBeforePreallocating(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 1, steps: 1, memoryQuota: 0}
	_, err := callArrayMember(t, exec, largeArrayPairReceiver(4000), "to_h", nil, NewNil())
	requireErrorIs(t, err, errStepQuotaExceeded)
	if exec.steps != 1 {
		t.Fatalf("expected to_h to abort before preallocating the output map (steps unchanged at 1), but steps reached %d", exec.steps)
	}
}

// TestArrayToHashRejectsInsufficientStepQuotaBeforePreallocating covers the
// positive-but-insufficient quota case: even when one step is available, a
// receiver longer than the remaining quota cannot complete, so to_h must abort
// before reserving the full output map.
func TestArrayToHashRejectsInsufficientStepQuotaBeforePreallocating(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 40, memoryQuota: 0}
	_, err := callArrayMember(t, exec, largeArrayPairReceiver(4000), "to_h", nil, NewNil())
	requireErrorIs(t, err, errStepQuotaExceeded)
	if exec.steps != 0 {
		t.Fatalf("expected to_h to abort before preallocating the output map (0 steps), but steps reached %d", exec.steps)
	}
}

// TestHashToArrayRejectsSlotBackingUpFront pins the slice-before-allocate half of
// the P2 finding: Hash#to_a's make([]Value, 0, len(keys)) reserves the whole slot
// backing in one allocation before the first per-pair charge could observe it, so
// a receiver that fits the quota but whose output slice does not could transiently
// exceed MemoryQuotaBytes. The build must be rejected before that allocation, not
// after. Sizing the quota to admit the receiver and the sorted-key scratch but not
// the full slot backing makes the up-front reserveSlots the only thing that can
// reject the build.
//
// The per-pair acc.add charges cap(pairs) (= len(keys) once make has reserved it),
// so it would also report the over-quota condition; the distinguishing signal is
// that reserveSlots rejects BEFORE the loop runs its first exec.step(), so no step
// is consumed. Asserting exec.steps stayed 0 pins the up-front rejection: without
// it the first step runs (and the full backing is already allocated) before add
// reports the error.
func TestHashToArrayRejectsSlotBackingUpFront(t *testing.T) {
	t.Parallel()

	const count = 4000
	receiver := largeHashReceiver(count)

	// Build the baseline the accumulator sees (call roots plus the scratch buffer)
	// with a probe, then size the quota to clear it with slim headroom while still
	// falling short of the full slot backing. The slot backing alone
	// (estimatedValueBytes per key plus the slice base) must overflow the headroom
	// so reserveSlots rejects before make allocates it.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, NewNil())
	baseline := roots + sortedKeyBufferBytes(count)
	slotBacking := estimatedValueBytes + estimatedSliceBaseBytes + count*estimatedValueBytes
	quota := baseline + slotBacking/4
	if quota >= baseline+slotBacking {
		t.Fatalf("test setup expects the slot backing (%d) to exceed the headroom above the baseline (%d)", slotBacking, quota-baseline)
	}

	fits := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	if err := fits.checkCallMemoryRoots(receiver, nil, nil, NewNil()); err != nil {
		t.Fatalf("receiver should fit under quota %d: %v", quota, err)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "to_a", nil, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps != 0 {
		t.Fatalf("expected the slot backing to be rejected before the loop allocates it (0 steps), but %d step(s) ran", exec.steps)
	}
}

// callArrayMember resolves an array member builtin and invokes it directly so the
// conversion tests can supply a controlled Execution, mirroring callHashMember.
// The builtins are pure functions of (exec, receiver, args, kwargs, block).
func callArrayMember(t *testing.T, exec *Execution, receiver Value, name string, args []Value, block Value) (Value, error) {
	t.Helper()
	member, err := arrayMember(receiver, name)
	if err != nil {
		t.Fatalf("arrayMember(%s): %v", name, err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("array member %s is not a builtin", name)
	}
	return builtin.Fn(exec, receiver, args, nil, block)
}

// toHPairBlock builds the block { |k| [<key>, <value>] }, where key and value are
// each either the block parameter (the per-element receiver value) or a captured
// constant. It lets a test choose, per side, whether the entry's key or value is a
// distinct receiver-derived payload or a shared constant, isolating which half of
// the build accumulator's charge a quota trips on.
func toHPairBlock(keyFromParam bool, capturedKey Value, valueFromParam bool, capturedValue Value) Value {
	pos := ast.Position{Line: 1, Column: 1}
	env := newEnv(nil)
	keyExpr := pairElementExpr(keyFromParam, "capturedKey", capturedKey, env, pos)
	valueExpr := pairElementExpr(valueFromParam, "capturedValue", capturedValue, env, pos)
	body := []ast.Statement{&ast.ExprStmt{Position: pos, Expr: &ast.ArrayLiteral{
		Position: pos,
		Elements: []ast.Expression{keyExpr, valueExpr},
	}}}
	return NewBlock([]ast.Param{{Name: "k"}}, body, env)
}

func pairElementExpr(fromParam bool, captureName string, captured Value, env *Env, pos ast.Position) ast.Expression {
	if fromParam {
		return &ast.Identifier{Name: "k", Position: pos}
	}
	env.Define(captureName, captured)
	return &ast.Identifier{Name: captureName, Position: pos}
}

func transientToHPairBlock(pairCap int) Value {
	pos := ast.Position{Line: 1, Column: 1}
	env := newEnv(nil)
	env.Define("make_pair", NewBuiltin("make_pair", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
		return freshToHPair(pairCap), nil
	}))
	body := []ast.Statement{&ast.ExprStmt{Position: pos, Expr: &ast.CallExpr{
		Position: pos,
		Callee:   &ast.Identifier{Name: "make_pair", Position: pos},
	}}}
	return NewBlock([]ast.Param{{Name: "k"}}, body, env)
}

func freshToHPair(pairCap int) Value {
	pair := make([]Value, 2, pairCap)
	pair[0] = NewSymbol("")
	pair[1] = NewInt(1)
	return NewArray(pair)
}

// TestArrayToHashBlockChargesSynthesizedKeys pins the P1 finding on this PR: the
// block form's synthesized keys must be charged as entries are inserted, not left
// to a post-call check. A block that maps each large distinct element to a fresh
// key, value pair (k.to_h-style { |k| [k, 7] }) builds keys that live only in the
// Go-local output map until the builtin returns. The structural projection only
// reserves one slot per element and the receiver dedups against the call roots, so
// without the accumulator the build runs to completion and returns an over-quota
// map. Driving the builtin directly removes the statement-level post-call net, so
// the only thing that can reject the over-quota build is the incremental charge.
func TestArrayToHashBlockChargesSynthesizedKeys(t *testing.T) {
	t.Parallel()

	const count = 300
	const keyLen = 2048
	items := make([]Value, count)
	for i := range count {
		items[i] = NewString(strings.Repeat("k", keyLen) + strconv.Itoa(i))
	}
	receiver := NewArray(items)
	// { |k| [k, 7] }: each entry's key is the distinct large element, its value a
	// shared small constant. The keys accumulate; the value contributes nothing.
	block := toHPairBlock(true, NewNil(), false, NewInt(7))

	// The structural projection dedups the receiver against the call roots, so size
	// the quota above it (the receiver and one slot per element fit) but below the
	// summed key payloads, leaving the accumulator's per-key charge the only thing
	// that can reject the build.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	structural := roots + estimatedEmptyOutputHashBytes + count*estimatedMapEntryStructuralBytes
	quota := structural + 64*1024
	if quota >= structural+count*keyLen {
		t.Fatalf("test setup expects the summed key payloads (%d) to exceed the headroom above the structural projection (%d)", count*keyLen, quota-structural)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, exec, receiver, "to_h", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestArrayToHashBlockChargesSynthesizedValues is the value-side twin: a block
// that collapses every element onto one key while returning a distinct large value
// per element ({ |k| ["dup", k] }). The final map holds a single entry, so even a
// post-call check on the result would see almost nothing, yet the transient values
// the block produces accumulate well past the quota during the build. The
// accumulator charges each value per write (the conservative over-count that keeps
// the bound sound), so the build is rejected as the values accrue.
func TestArrayToHashBlockChargesSynthesizedValues(t *testing.T) {
	t.Parallel()

	const count = 300
	const valLen = 2048
	items := make([]Value, count)
	for i := range count {
		items[i] = NewString(strings.Repeat("v", valLen) + strconv.Itoa(i))
	}
	receiver := NewArray(items)
	// { |k| ["dup", k] }: all entries collapse onto the constant key "dup" while
	// each value is the distinct large element, so the values accumulate even though
	// the final map holds one entry.
	block := toHPairBlock(false, NewSymbol("dup"), true, NewNil())

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	structural := roots + estimatedEmptyOutputHashBytes + count*estimatedMapEntryStructuralBytes
	quota := structural + 64*1024
	if quota >= structural+count*valLen {
		t.Fatalf("test setup expects the summed value payloads (%d) to exceed the headroom above the structural projection (%d)", count*valLen, quota-structural)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := callArrayMember(t, exec, receiver, "to_h", nil, block)
	if err == nil {
		t.Fatalf("expected the accumulated block values to trip the quota, but the build produced a hash with %d entries", len(got.Hash()))
	}
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestArrayToHashBlockChargesTransientPair pins the peak-memory half of the block
// form: the block's returned two-element pair is live while the preallocated
// output map backing is also live, even though the final hash retains only the
// pair's key and value. Each piece fits alone; the combined peak must not.
func TestArrayToHashBlockChargesTransientPair(t *testing.T) {
	t.Parallel()

	const pairCap = 4096
	receiver := NewArray([]Value{NewInt(1)})
	block := transientToHPairBlock(pairCap)

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	output := roots + estimatedEmptyOutputHashBytes + estimatedMapEntryStructuralBytes
	pairBytes := newMemoryEstimator().value(freshToHPair(pairCap))
	quota := output + pairBytes - 1
	if quota <= output {
		t.Fatalf("test setup expects transient pair bytes (%d) to leave a quota above output-only bytes (%d)", pairBytes, output)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, exec, receiver, "to_h", nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashToArrayHonorsMemoryQuota pins the P2 finding on this PR: a hash whose
// receiver fits the quota but whose [key, value] materialization does not must be
// rejected as the pairs accumulate, not after the whole array is built. Driving
// the builtin directly removes the statement-level post-call net (which would mask
// an unbounded build behind a single check on the finished result), so the only
// thing that can reject the over-quota materialization is the incremental charge
// the accumulator and the per-pair step now perform during the loop.
func TestHashToArrayHonorsMemoryQuota(t *testing.T) {
	t.Parallel()

	const count = 4000
	receiver := largeHashReceiver(count)

	// Size the quota to admit the receiver but not the pair materialization on top:
	// the output slice, one [key, value] pair array per entry, and the sorted-key
	// scratch buffer push the build over a quota with slim receiver headroom.
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, NewNil())
	quota := roots + roots/4

	fits := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	if err := fits.checkCallMemoryRoots(receiver, nil, nil, NewNil()); err != nil {
		t.Fatalf("receiver should fit under quota %d: %v", quota, err)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callHashMember(t, exec, receiver, "to_a", nil, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestHashToArrayHonorsStepQuota pins the second half of the P2 finding: the
// materialization must charge a step per pair so a small step quota stops it
// mid-loop. Before the fix the loop emitted every pair without a step charge, so a
// large hash ignored the step quota entirely during materialization.
func TestHashToArrayHonorsStepQuota(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 40, memoryQuota: 64 << 20}
	_, err := callHashMember(t, exec, largeHashReceiver(2_000), "to_a", nil, NewNil())
	requireErrorIs(t, err, errStepQuotaExceeded)
}

// TestHashToArrayHonorsCancellation verifies the per-pair step check observes a
// canceled context, so a sandboxed to_a cannot keep materializing after
// cancellation. step polls the context on its first call, so even a tiny hash
// aborts.
func TestHashToArrayHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := &Execution{ctx: ctx, quota: 1 << 30, memoryQuota: 0}
	_, err := callHashMember(t, exec, largeHashReceiver(8), "to_a", nil, NewNil())
	requireErrorIs(t, err, context.Canceled)
}

// TestHashToArrayChecksBudgetBeforeSorting pins the up-front half of the P2
// finding: with a large hash, an already-canceled context, and no memory quota,
// Hash#to_a must abort before materializing and sorting the keys (an O(n log n)
// cost plus a scratch list) and before the make reserves the full output slice.
// The per-pair loop's first exec.step does poll the canceled context, but only
// after the sort and the slot backing have already run. Asserting exec.steps
// stayed 0 distinguishes the up-front checkStepBudgetFor from the in-loop step: without
// the guard the loop runs its first step (after the sort) and steps reaches 1.
func TestHashToArrayChecksBudgetBeforeSorting(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := &Execution{ctx: ctx, quota: 1 << 30, memoryQuota: 0}
	_, err := callHashMember(t, exec, largeHashReceiver(4000), "to_a", nil, NewNil())
	requireErrorIs(t, err, context.Canceled)
	if exec.steps != 0 {
		t.Fatalf("expected to_a to abort before sorting the keys (0 steps), but %d step(s) ran", exec.steps)
	}
}

// TestHashToArrayChecksStepQuotaBeforeSorting is the step-quota twin: an
// already-spent step quota (steps == quota) must abort Hash#to_a before the sort
// and the output-slice allocation. Pre-spending the quota and asserting steps
// never advances past it pins the up-front checkStepBudgetFor; without it the in-loop
// step would increment past the quota before reporting the error, by which point
// the sort and the full slot backing have already run.
func TestHashToArrayChecksStepQuotaBeforeSorting(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 1, steps: 1, memoryQuota: 0}
	_, err := callHashMember(t, exec, largeHashReceiver(4000), "to_a", nil, NewNil())
	requireErrorIs(t, err, errStepQuotaExceeded)
	if exec.steps != 1 {
		t.Fatalf("expected to_a to abort before sorting the keys (steps unchanged at 1), but steps reached %d", exec.steps)
	}
}

// TestHashToArrayRejectsInsufficientStepQuotaBeforeSorting pins the exact P2
// finding: a positive but insufficient remaining step quota (for example 40
// remaining steps on a hash with thousands of entries) must abort Hash#to_a
// before materializing and sorting the keys. The per-pair loop charges one step
// per entry, so a remaining quota smaller than len(entries) guarantees the loop
// fails partway; without bounding the up-front check on len(entries) the sort
// (O(n log n) CPU plus a scratch list) and the output-slice allocation run first,
// letting a sandboxed call spend that work on a projection that can never fit.
// Asserting exec.steps stayed 0 proves the sort was skipped: with only the
// remaining > 0 check the loop would run its first step (after the sort) and steps
// would reach 1.
func TestHashToArrayRejectsInsufficientStepQuotaBeforeSorting(t *testing.T) {
	t.Parallel()

	const count = 4000
	exec := &Execution{ctx: context.Background(), quota: 40, memoryQuota: 64 << 20}
	_, err := callHashMember(t, exec, largeHashReceiver(count), "to_a", nil, NewNil())
	requireErrorIs(t, err, errStepQuotaExceeded)
	if exec.steps != 0 {
		t.Fatalf("expected to_a to abort before sorting the keys (0 steps) when only %d of %d required steps remain, but %d step(s) ran", exec.quota, count, exec.steps)
	}
}
