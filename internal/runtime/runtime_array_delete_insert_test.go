package runtime

import "testing"

func TestArrayDelete(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def delete_value(values, target)
      values.delete(target)
    end

    def delete_with_default(values, target)
      values.delete(target) { "missing" }
    end

    def delete_with_param(values, target)
      values.delete(target) { |o| o }
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
			result := callFunc(t, script, tt.function, tt.args)
			if result.Kind() != KindHash {
				t.Fatalf("expected hash result, got %v", result.Kind())
			}
			res := result.Hash()
			compareArrays(t, res["array"], tt.wantArray)
			if diff := valueDiff(tt.wantDeleted, res["deleted"]); diff != "" {
				t.Fatalf("deleted mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestArrayDeleteIsNonMutating(t *testing.T) {
	t.Parallel()
	// delete mirrors pop: it returns a new array and leaves the receiver
	// untouched, matching Vibescript's non-mutating collection model.
	script := compileScript(t, `
    def delete_preserves_source(values, target)
      removed = values.delete(target)
      { source: values, removed: removed }
    end
    `)

	result := callFunc(t, script, "delete_preserves_source",
		[]Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(2)}), NewInt(2)}).Hash()
	compareArrays(t, result["source"], []Value{NewInt(1), NewInt(2), NewInt(2)})
	removed := result["removed"].Hash()
	compareArrays(t, removed["array"], []Value{NewInt(1)})
	if diff := valueDiff(NewInt(2), removed["deleted"]); diff != "" {
		t.Fatalf("deleted mismatch (-want +got):\n%s", diff)
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
      result = [stored].delete(search)
      deleted = result[:deleted]
      deleted[0] = 999
      { deleted: deleted, search: search }
    end

    def delete_returns_last_match
      first = ["x"]
      last = ["x"]
      search = ["x"]
      result = [first, last].delete(search)
      deleted = result[:deleted]
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
	// Ruby returns the last deleted element, so mutating the result touches the
	// last equal element rather than the first.
	compareArrays(t, lastMatch["first"], []Value{NewString("x")})
	compareArrays(t, lastMatch["last"], []Value{NewString("mutated")})
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
	script := compileScript(t, `
    def shift_one(values)
      values.shift
    end

    def shift_n(values, n)
      values.shift(n)
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
			result := callFunc(t, script, tt.function, tt.args)
			if result.Kind() != KindHash {
				t.Fatalf("expected hash result, got %v", result.Kind())
			}
			res := result.Hash()
			compareArrays(t, res["array"], tt.wantArray)
			if diff := valueDiff(tt.wantShifted, res["shifted"]); diff != "" {
				t.Fatalf("shifted mismatch (-want +got):\n%s", diff)
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
	// unshift is a Ruby alias for prepend: it inserts the arguments, in order, at
	// the front and returns a new array.
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

func TestArrayInsertIsNonMutating(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def insert_preserves_source(values, value)
      inserted = values.insert(1, value)
      { source: values, inserted: inserted }
    end
    `)

	result := callFunc(t, script, "insert_preserves_source",
		[]Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewString("x")}).Hash()
	compareArrays(t, result["source"], []Value{NewInt(1), NewInt(2)})
	compareArrays(t, result["inserted"], []Value{NewInt(1), NewString("x"), NewInt(2)})
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

// TestArrayHelpersDoNotAliasReceiverBacking guards every collection helper this
// PR adds or touches against the non-mutating contract: a returned result array
// must never share the receiver's backing slice. Vibescript arrays are mutable
// through index assignment (arr[i] = v writes straight into the backing slice),
// so each case index-assigns into the returned array and then asserts the source
// receiver is unchanged. compareArrays only inspects values and cannot catch
// aliasing, so the in-script mutation is what makes this regression meaningful.
func TestArrayHelpersDoNotAliasReceiverBacking(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def shift_zero(values)
      result = values.shift(0)
      out = result[:array]
      out[0] = 999
      values
    end

    def pop_zero(values)
      result = values.pop(0)
      out = result[:array]
      out[0] = 999
      values
    end

    def shift_count(values)
      result = values.shift(1)
      out = result[:array]
      out[0] = 999
      values
    end

    def pop_count(values)
      result = values.pop(1)
      out = result[:array]
      out[0] = 999
      values
    end

    def delete_miss(values)
      result = values.delete(999)
      out = result[:array]
      out[0] = 999
      values
    end

    def delete_hit(values)
      result = values.delete(2)
      out = result[:array]
      out[0] = 999
      values
    end

    def insert_noop(values)
      out = values.insert(1)
      out[0] = 999
      values
    end

    def insert_values(values)
      out = values.insert(1, "x")
      out[0] = 999
      values
    end

    def prepend_noop(values)
      out = values.prepend
      out[0] = 999
      values
    end

    def unshift_noop(values)
      out = values.unshift
      out[0] = 999
      values
    end
    `)

	tests := []struct {
		name     string
		function string
	}{
		{"shift(0) copies the receiver", "shift_zero"},
		{"pop(0) copies the receiver", "pop_zero"},
		{"shift(n) copies the receiver", "shift_count"},
		{"pop(n) copies the receiver", "pop_count"},
		{"delete on a miss copies the receiver", "delete_miss"},
		{"delete on a hit copies the receiver", "delete_hit"},
		{"insert with no values copies the receiver", "insert_noop"},
		{"insert with values copies the receiver", "insert_values"},
		{"prepend with no values copies the receiver", "prepend_noop"},
		{"unshift with no values copies the receiver", "unshift_noop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			source := callFunc(t, script, tt.function,
				[]Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})})
			compareArrays(t, source, []Value{NewInt(1), NewInt(2), NewInt(3)})
		})
	}
}
