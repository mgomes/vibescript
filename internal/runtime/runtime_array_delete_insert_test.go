package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestArrayDelete(t *testing.T) {
	t.Parallel()
	// Ruby contract: delete removes every matching element from the receiver
	// in place and returns the removed element (or the block result / nil on a
	// miss). Each helper reports both halves so the receiver state is pinned.
	script := compileScript(t, `
    def delete_value(values, target)
      { removed: values.delete(target), values: values }
    end

    def delete_with_default(values, target)
      { removed: values.delete(target) { "missing" }, values: values }
    end

    def delete_with_param(values, target)
      { removed: values.delete(target) { |o| o }, values: values }
    end
    `)

	tests := []struct {
		name        string
		function    string
		args        []Value
		wantArray   []Value
		wantDeleted Value
	}{
		{
			name:        "removes every matching element and reports the value",
			function:    "delete_value",
			args:        []Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(2), NewInt(3)}), NewInt(2)},
			wantArray:   []Value{NewInt(1), NewInt(3)},
			wantDeleted: NewInt(2),
		},
		{
			name:        "reports nil when the value is absent",
			function:    "delete_value",
			args:        []Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}), NewInt(9)},
			wantArray:   []Value{NewInt(1), NewInt(2), NewInt(3)},
			wantDeleted: NewNil(),
		},
		{
			name:        "empties an array whose elements all match",
			function:    "delete_value",
			args:        []Value{NewArray([]Value{NewInt(1), NewInt(1), NewInt(1)}), NewInt(1)},
			wantArray:   []Value{},
			wantDeleted: NewInt(1),
		},
		{
			name:        "block result is reported on a miss",
			function:    "delete_with_default",
			args:        []Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(9)},
			wantArray:   []Value{NewInt(1), NewInt(2)},
			wantDeleted: NewString("missing"),
		},
		{
			name:        "block is ignored when the value is found",
			function:    "delete_with_default",
			args:        []Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(2)},
			wantArray:   []Value{NewInt(1)},
			wantDeleted: NewInt(2),
		},
		{
			// Ruby passes the searched-for value to the not-found block, so
			// [1,2].delete(9) { |o| o } yields 9 rather than nil.
			name:        "block receives the searched-for value on a miss",
			function:    "delete_with_param",
			args:        []Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(9)},
			wantArray:   []Value{NewInt(1), NewInt(2)},
			wantDeleted: NewInt(9),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := callFunc(t, script, tt.function, tt.args).Hash()
			compareArrays(t, res["values"], tt.wantArray)
			if diff := valueDiff(tt.wantDeleted, res["removed"]); diff != "" {
				t.Fatalf("removed mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestArrayDeleteUpdatesItsRoot(t *testing.T) {
	t.Parallel()
	// delete updates the local it addresses, and a binding taken before the
	// call keeps the value it was given.
	script := compileScript(t, `
    def delete_mutates_source(values, target)
      other = values
      removed = values.delete(target)
      { source: values, other: other, removed: removed }
    end
    `)

	result := callFunc(t, script, "delete_mutates_source",
		[]Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(2)}), NewInt(2)}).Hash()
	compareArrays(t, result["source"], []Value{NewInt(1)})
	compareArrays(t, result["other"], []Value{NewInt(1), NewInt(2), NewInt(2)})
	if diff := valueDiff(NewInt(2), result["removed"]); diff != "" {
		t.Fatalf("removed mismatch (-want +got):\n%s", diff)
	}
}

// TestArrayDeleteReturnsStoredElement guards that delete reports the element
// actually removed rather than the caller's search argument. Ruby's Array#delete
// returns the deleted object, so when a stored element is Equal to but a distinct
// object from the argument the caller must get back the stored element. Vibescript
// arrays are mutable through index assignment, so the test mutates the returned
// deleted element and asserts the separately built search argument is untouched:
// that can only hold if delete returned the stored element, not the argument.
func TestArrayDeleteReturnsStoredElement(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def delete_nested
      stored = [1, 2]
      search = [1, 2]
      deleted = [stored].delete(search)
      deleted[0] = 999
      { deleted: deleted, search: search }
    end

    def delete_returns_last_match
      first = ["x"]
      last = ["x"]
      search = ["x"]
      deleted = [first, last].delete(search)
      deleted[0] = "mutated"
      { first: first, last: last }
    end
    `)

	nested := callFunc(t, script, "delete_nested", nil).Hash()
	compareArrays(t, nested["deleted"], []Value{NewInt(999), NewInt(2)})
	// The search argument must be untouched; mutating the returned element only
	// affects the search value when delete wrongly returns the argument.
	compareArrays(t, nested["search"], []Value{NewInt(1), NewInt(2)})

	lastMatch := callFunc(t, script, "delete_returns_last_match", nil).Hash()
	// The removed element is returned as a value, so writing through it reaches
	// neither of the locals the temporary array was built from.
	compareArrays(t, lastMatch["first"], []Value{NewString("x")})
	compareArrays(t, lastMatch["last"], []Value{NewString("x")})
}

func TestArrayDeleteMissTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	const count = 20_000
	receiver := largeIntArray(count)
	target := NewInt(-1)
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 62}
	acc := newArrayBuildAccumulator(probe, receiver, []Value{target}, nil, NewNil())
	quota := acc.projected(count) - 1
	if acc.base > quota {
		t.Fatalf("test setup call roots = %d exceed quota = %d", acc.base, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := arrayDelete(exec, receiver, []Value{target}, nil, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

func TestArrayDeleteAllMatchesFitsBelowFullCopyQuota(t *testing.T) {
	t.Parallel()

	const count = 20_000
	items := make([]Value, count)
	for i := range items {
		items[i] = NewInt(1)
	}
	receiver := NewArray(items)
	target := NewInt(1)
	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 62}
	acc := newArrayBuildAccumulator(probe, receiver, []Value{target}, nil, NewNil())
	quota := acc.projected(count) - 1
	if acc.base > quota {
		t.Fatalf("test setup call roots = %d exceed quota = %d", acc.base, quota)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := arrayDelete(exec, receiver, []Value{target}, nil, NewNil())
	if err != nil {
		t.Fatalf("array.delete with every element removed under sparse quota = %v, want success", err)
	}
	if diff := valueDiff(NewInt(1), got); diff != "" {
		t.Fatalf("removed mismatch (-want +got):\n%s", diff)
	}
	compareArrays(t, receiver, []Value{})
}

func TestArrayDeleteAllMatchesHonorsStepQuota(t *testing.T) {
	t.Parallel()

	const count = 1_000
	items := make([]Value, count)
	for i := range items {
		items[i] = NewInt(1)
	}

	exec := &Execution{ctx: context.Background(), quota: 64, memoryQuota: 1 << 30}
	_, err := arrayDelete(exec, NewArray(items), []Value{NewInt(1)}, nil, NewNil())
	requireErrorIs(t, err, errStepQuotaExceeded)
}

func TestArrayDeleteErrors(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def no_args(values)
      values.delete
    end

    def too_many(values)
      values.delete(1, 2)
    end

    def keyword(values)
      values.delete(foo: 1)
    end
    `)

	base := []Value{NewArray([]Value{NewInt(1)})}
	requireCallErrorContains(t, script, "no_args", base, CallOptions{},
		"array.delete expects exactly one value")
	requireCallErrorContains(t, script, "too_many", base, CallOptions{},
		"array.delete expects exactly one value")
	requireCallErrorContains(t, script, "keyword", base, CallOptions{},
		"array.delete does not take keyword arguments")
}

func TestArrayShift(t *testing.T) {
	t.Parallel()
	// Ruby contract: shift removes elements from the front of the receiver in
	// place. Bare shift returns the removed element (nil on an empty array);
	// shift(n) returns the removed prefix as an array.
	script := compileScript(t, `
    def shift_one(values)
      { removed: values.shift, values: values }
    end

    def shift_n(values, n)
      { removed: values.shift(n), values: values }
    end
    `)

	tests := []struct {
		name        string
		function    string
		args        []Value
		wantArray   []Value
		wantShifted Value
	}{
		{
			name:        "removes the first element and reports it",
			function:    "shift_one",
			args:        []Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})},
			wantArray:   []Value{NewInt(2), NewInt(3)},
			wantShifted: NewInt(1),
		},
		{
			name:        "reports nil on an empty array",
			function:    "shift_one",
			args:        []Value{NewArray([]Value{})},
			wantArray:   []Value{},
			wantShifted: NewNil(),
		},
		{
			name:        "shift(n) removes the leading n as an array",
			function:    "shift_n",
			args:        []Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}), NewInt(2)},
			wantArray:   []Value{NewInt(3)},
			wantShifted: NewArray([]Value{NewInt(1), NewInt(2)}),
		},
		{
			name:        "shift(0) removes nothing but returns an empty array",
			function:    "shift_n",
			args:        []Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(0)},
			wantArray:   []Value{NewInt(1), NewInt(2)},
			wantShifted: NewArray([]Value{}),
		},
		{
			name:        "shift(n) clamps to the array length",
			function:    "shift_n",
			args:        []Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(5)},
			wantArray:   []Value{},
			wantShifted: NewArray([]Value{NewInt(1), NewInt(2)}),
		},
		{
			name:        "shift(n) on an empty array reports an empty array",
			function:    "shift_n",
			args:        []Value{NewArray([]Value{}), NewInt(2)},
			wantArray:   []Value{},
			wantShifted: NewArray([]Value{}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := callFunc(t, script, tt.function, tt.args).Hash()
			compareArrays(t, res["values"], tt.wantArray)
			if diff := valueDiff(tt.wantShifted, res["removed"]); diff != "" {
				t.Fatalf("removed mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestArrayShiftErrors(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def negative(values)
      values.shift(-1)
    end

    def non_integer(values)
      values.shift("two")
    end

    def too_many(values)
      values.shift(1, 2)
    end

    def keyword(values)
      values.shift(foo: 1)
    end
    `)

	base := []Value{NewArray([]Value{NewInt(1), NewInt(2)})}
	requireCallErrorContains(t, script, "negative", base, CallOptions{},
		"array.shift expects non-negative integer")
	requireCallErrorContains(t, script, "non_integer", base, CallOptions{},
		"array.shift expects non-negative integer")
	requireCallErrorContains(t, script, "too_many", base, CallOptions{},
		"array.shift accepts at most one argument")
	requireCallErrorContains(t, script, "keyword", base, CallOptions{},
		"array.shift does not take keyword arguments")
}

func TestArrayUnshift(t *testing.T) {
	t.Parallel()
	// unshift is a Ruby alias for prepend: it inserts the arguments, in order,
	// at the front of the receiver in place and returns the receiver.
	script := compileScript(t, `
    def unshift_values(values, a, b)
      values.unshift(a, b)
    end

    def unshift_none(values)
      values.unshift
    end
    `)

	tests := []struct {
		name     string
		function string
		args     []Value
		want     []Value
	}{
		{
			name:     "inserts values in order at the front",
			function: "unshift_values",
			args:     []Value{NewArray([]Value{NewInt(3)}), NewInt(1), NewInt(2)},
			want:     []Value{NewInt(1), NewInt(2), NewInt(3)},
		},
		{
			name:     "unshift onto an empty array",
			function: "unshift_values",
			args:     []Value{NewArray([]Value{}), NewInt(1), NewInt(2)},
			want:     []Value{NewInt(1), NewInt(2)},
		},
		{
			name:     "unshift with no values returns the array unchanged",
			function: "unshift_none",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2)})},
			want:     []Value{NewInt(1), NewInt(2)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			compareArrays(t, callFunc(t, script, tt.function, tt.args), tt.want)
		})
	}
}

func TestArrayUnshiftRejectsKeywordArguments(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def unshift_keyword(values)
      values.unshift(foo: 1)
    end
    `)
	requireCallErrorContains(t, script, "unshift_keyword",
		[]Value{NewArray([]Value{NewInt(1)})}, CallOptions{},
		"array.unshift does not take keyword arguments")
}

func TestArrayInsert(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def insert_at(values, index, value)
      values.insert(index, value)
    end

    def insert_many(values, index, a, b)
      values.insert(index, a, b)
    end

    def insert_index_only(values, index)
      values.insert(index)
    end
    `)

	tests := []struct {
		name     string
		function string
		args     []Value
		want     []Value
	}{
		{
			name:     "inserts before the element at a positive index",
			function: "insert_at",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}), NewInt(1), NewString("x")},
			want:     []Value{NewInt(1), NewString("x"), NewInt(2), NewInt(3)},
		},
		{
			name:     "inserts at the front with index zero",
			function: "insert_at",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(0), NewString("x")},
			want:     []Value{NewString("x"), NewInt(1), NewInt(2)},
		},
		{
			name:     "inserts at the end with the length as index",
			function: "insert_at",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(2), NewString("x")},
			want:     []Value{NewInt(1), NewInt(2), NewString("x")},
		},
		{
			name:     "pads with nil when the index is past the end",
			function: "insert_at",
			args:     []Value{NewArray([]Value{NewInt(1)}), NewInt(3), NewString("x")},
			want:     []Value{NewInt(1), NewNil(), NewNil(), NewString("x")},
		},
		{
			name:     "negative index inserts after that element",
			function: "insert_at",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}), NewInt(-2), NewString("x")},
			want:     []Value{NewInt(1), NewInt(2), NewString("x"), NewInt(3)},
		},
		{
			name:     "index minus one appends",
			function: "insert_at",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(-1), NewString("x")},
			want:     []Value{NewInt(1), NewInt(2), NewString("x")},
		},
		{
			name:     "most negative valid index inserts at the front",
			function: "insert_at",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(-3), NewString("x")},
			want:     []Value{NewString("x"), NewInt(1), NewInt(2)},
		},
		{
			name:     "inserts multiple values in order",
			function: "insert_many",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(4)}), NewInt(1), NewInt(2), NewInt(3)},
			want:     []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)},
		},
		{
			name:     "index only returns the array unchanged",
			function: "insert_index_only",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(0)},
			want:     []Value{NewInt(1), NewInt(2)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			compareArrays(t, callFunc(t, script, tt.function, tt.args), tt.want)
		})
	}
}

func TestArrayInsertMutatesReceiver(t *testing.T) {
	t.Parallel()
	// insert splices into the receiver in place and returns the receiver
	// itself, matching Ruby's Array#insert.
	script := compileScript(t, `
    def insert_mutates_source(values, value)
      inserted = values.insert(1, value)
      { source: values, inserted: inserted, same: inserted.equal?(values) }
    end
    `)

	result := callFunc(t, script, "insert_mutates_source",
		[]Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewString("x")}).Hash()
	compareArrays(t, result["source"], []Value{NewInt(1), NewString("x"), NewInt(2)})
	compareArrays(t, result["inserted"], []Value{NewInt(1), NewString("x"), NewInt(2)})
	if !result["same"].Bool() {
		t.Fatal("insert must return the receiver itself")
	}
}

func TestArrayInsertErrors(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def no_args(values)
      values.insert
    end

    def non_integer_index(values)
      values.insert("one", 2)
    end

    def out_of_range(values)
      values.insert(-4, 9)
    end

    def keyword(values)
      values.insert(0, foo: 1)
    end
    `)

	base := []Value{NewArray([]Value{NewInt(1), NewInt(2)})}
	requireCallErrorContains(t, script, "no_args", base, CallOptions{},
		"array.insert expects an index")
	requireCallErrorContains(t, script, "non_integer_index", base, CallOptions{},
		"array.insert index must be integer")
	requireCallErrorContains(t, script, "out_of_range", base, CallOptions{},
		"array.insert index -4 out of range")
	requireCallErrorContains(t, script, "keyword", base, CallOptions{},
		"array.insert does not take keyword arguments")
}

func TestInsertRejectsOutOfRangeBeforeIsolating(t *testing.T) {
	t.Parallel()
	skipNoCopyPin(t)

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 240 << 10, StepQuota: Unlimited}, `
    def run()
      a = []
      i = 0
      while i < 2000
        a << "xxxxxxxxxxxxxxxx"
        i += 1
      end
      b = a
      a.insert(-1000000, 1)
    end
    `)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatal("a.insert(-1000000, 1) must error")
	}
	requireErrorContains(t, err, "array.insert index -1000000 out of range")
	if strings.Contains(err.Error(), "quota") {
		t.Fatalf("out-of-range insert reported as quota: %v", err)
	}
}

// TestArrayInsertMemoryQuota confirms a nil-padded growth far past the end trips
// the memory quota up front instead of reserving a huge backing array.
func TestArrayInsertMemoryQuota(t *testing.T) {
	t.Parallel()
	source := `def run()
  [1].insert(9000000000000000, "x")
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 * 1024}, source)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

// TestArrayInsertStepQuota confirms the per-slot stepping of the nil pad keeps a
// growth far past the end bounded by the step quota even when the memory quota is
// generous.
func TestArrayInsertStepQuota(t *testing.T) {
	t.Parallel()
	source := `def run()
  [1].insert(1000000, "x")
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 64, MemoryQuotaBytes: 1 << 30}, source)
	requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
}

func TestArrayInsertCountsLiveCallRoots(t *testing.T) {
	t.Parallel()
	const receiverSize = 4096

	receiver := largeIntArray(receiverSize)
	args := []Value{NewInt(0), NewInt(-1)}
	probe := &Execution{}
	roots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, args, nil, NewNil())
	resultSlots := saturatingAdd(
		estimatedValueBytes+estimatedSliceBaseBytes,
		saturatingMul(receiverSize+1, estimatedValueBytes),
	)
	quota := saturatingAdd(roots, resultSlots) - estimatedValueBytes

	fits := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	if err := fits.checkCallMemoryRoots(receiver, args, nil, NewNil()); err != nil {
		t.Fatalf("receiver and args should fit under quota %d: %v", quota, err)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := callArrayMember(t, exec, receiver, "insert", args, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps != 0 {
		t.Fatalf("steps = %d, want insert rejected before building the result", exec.steps)
	}
}

// TestArrayNoopRemovalAllocatesNoReceiverCopy pins that the in-place pop(0)
// and shift(0) no longer copy the receiver: under a quota with no headroom for
// a second receiver-sized array they still succeed, return an empty array, and
// leave the receiver's contents untouched.
func TestArrayNoopRemovalAllocatesNoReceiverCopy(t *testing.T) {
	t.Parallel()
	const receiverSize = 20_000

	args := []Value{NewInt(0)}
	tests := []struct {
		name   string
		member string
	}{
		{"pop(0)", "pop"},
		{"shift(0)", "shift"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			receiver := largeIntArray(receiverSize)
			probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 62}
			acc := newArrayBuildAccumulator(probe, receiver, args, nil, NewNil())
			quota := acc.projected(receiverSize) - 1
			if acc.base > quota {
				t.Fatalf("test setup call roots = %d exceed quota = %d", acc.base, quota)
			}

			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
			got, err := callArrayMember(t, exec, receiver, tt.member, args, NewNil())
			if err != nil {
				t.Fatalf("%s under a copy-tight quota = %v, want success (no receiver copy)", tt.name, err)
			}
			compareArrays(t, got, []Value{})
			if length := len(receiver.Array()); length != receiverSize {
				t.Fatalf("receiver length after %s = %d, want %d", tt.name, length, receiverSize)
			}
		})
	}
}

// TestArrayRemovalResultsDoNotAliasReceiverBacking guards pop(n)/shift(n)
// against handing out arrays that share the receiver's backing slice: the
// removed elements are copied out, so index-assigning into the returned array
// never disturbs the (mutated) receiver, and later pushes onto the receiver
// never rewrite an escaped removal result.
func TestArrayRemovalResultsDoNotAliasReceiverBacking(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def shift_count(values)
      removed = values.shift(1)
      removed[0] = 999
      values
    end

    def pop_count(values)
      removed = values.pop(1)
      removed[0] = 999
      values
    end

    def pop_then_push(values)
      removed = values.pop(2)
      values.push(7)
      values.push(8)
      removed
    end
    `)

	source := callFunc(t, script, "shift_count",
		[]Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})})
	compareArrays(t, source, []Value{NewInt(2), NewInt(3)})

	source = callFunc(t, script, "pop_count",
		[]Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})})
	compareArrays(t, source, []Value{NewInt(1), NewInt(2)})

	// Pushing after a pop reuses the receiver's spare capacity; the escaped
	// removal result must hold its own copy of the removed elements.
	removed := callFunc(t, script, "pop_then_push",
		[]Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})})
	compareArrays(t, removed, []Value{NewInt(2), NewInt(3)})
}
