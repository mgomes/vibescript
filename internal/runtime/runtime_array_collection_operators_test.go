package runtime

import "testing"

func TestArrayShovelOperator(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def append_scalar()
      [1, 2] << 3
    end

    def append_array_element()
      [1, 2] << [3, 4]
    end

    def append_to_empty()
      [] << 1
    end

    def reassign_accumulator()
      values = [1, 2]
      values = values << 3
      values
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{
			name: "appends a scalar element",
			fn:   "append_scalar",
			want: NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		},
		{
			name: "appends an array as a single element",
			fn:   "append_array_element",
			want: NewArray([]Value{NewInt(1), NewInt(2), NewArray([]Value{NewInt(3), NewInt(4)})}),
		},
		{
			name: "appends to an empty array",
			fn:   "append_to_empty",
			want: NewArray([]Value{NewInt(1)}),
		},
		{
			name: "reassignment accumulates",
			fn:   "reassign_accumulator",
			want: NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, nil)
			if diff := valueDiff(tc.want, got); diff != "" {
				t.Fatalf("shovel mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestArrayShovelMutatesReceiver pins the Ruby contract: a bare "values << x"
// expression statement appends to the receiver in place and returns the
// receiver itself, so accumulation needs no reassignment and every alias
// observes the growth.
func TestArrayShovelMutatesReceiver(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def discarded()
      values = [1, 2]
      values << 3
      values
    end

    def returns_receiver()
      values = [1]
      (values << 2).equal?(values)
    end
    `)

	got := callFunc(t, script, "discarded", nil)
	want := NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})
	if diff := valueDiff(want, got); diff != "" {
		t.Fatalf("shovel mismatch (-want +got):\n%s", diff)
	}
	if got := callFunc(t, script, "returns_receiver", nil); !got.Bool() {
		t.Fatalf("(values << 2) is not the receiver; << must return self")
	}
}

// TestArrayShovelSharesAliases pins Ruby's reference semantics around <<: two
// variables bound to the same array both observe an in-place append, and the
// reassignment accumulator idiom (`out = out << x`) still works because <<
// returns the receiver.
func TestArrayShovelSharesAliases(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def accumulate(n)
      out = []
      for i in 1..n
        out = out << i
      end
      out
    end

    def alias_visibility()
      a = [1]
      b = a
      a = a << 2
      b[0] = 9
      { a: a, b: b }
    end

    def repeated_alias()
      a = []
      a = a << 1
      b = a
      a = a << 2
      b = b << 3
      { a: a, b: b }
    end
    `)

	compareArrays(t, callFunc(t, script, "accumulate", []Value{NewInt(5)}),
		[]Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4), NewInt(5)})

	// b aliases the same array object, so it sees the append, and a sees b's
	// element write: one object, two names, exactly as Ruby behaves.
	aliased := callFunc(t, script, "alias_visibility", nil).Hash()
	compareArrays(t, aliased["a"], []Value{NewInt(9), NewInt(2)})
	compareArrays(t, aliased["b"], []Value{NewInt(9), NewInt(2)})

	// Every << appends to the one shared object, so both names end bound to
	// the fully accumulated array.
	repeated := callFunc(t, script, "repeated_alias", nil).Hash()
	compareArrays(t, repeated["a"], []Value{NewInt(1), NewInt(2), NewInt(3)})
	compareArrays(t, repeated["b"], []Value{NewInt(1), NewInt(2), NewInt(3)})
}

// TestArrayShovelBlockResultsAliasReceiver pins the flip side of Ruby's
// reference semantics: a block returning `out << v` returns the receiver
// itself, so a map collecting those results holds aliases of one object and a
// later element write is visible through every one of them.
func TestArrayShovelBlockResultsAliasReceiver(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def block_results()
      out = []
      results = [1, 2].map do |v|
        out = out << v
      end
      second = results[1]
      second[0] = 9
      results
    end
    `)

	results := callFunc(t, script, "block_results", nil).Array()
	if len(results) != 2 {
		t.Fatalf("block_results length = %d, want 2", len(results))
	}
	compareArrays(t, results[0], []Value{NewInt(9), NewInt(2)})
	compareArrays(t, results[1], []Value{NewInt(9), NewInt(2)})
}

func TestArrayIntersectionOperator(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def basic()
      [1, 2, 3] & [2, 3, 4]
    end

    def removes_duplicates()
      [1, 1, 2, 3] & [1, 3, 4]
    end

    def preserves_left_order()
      [3, 1, 2] & [2, 1, 3]
    end

    def disjoint()
      [1, 2] & [3, 4]
    end

    def empty_left()
      [] & [1, 2]
    end

    def empty_right()
      [1, 2] & []
    end

    def mixed_types()
      [1, "a", :b] & ["a", :b, 9]
    end

    def nested_values()
      [[1], [2], [3]] & [[2], [3], [4]]
    end

    def locals()
      left = [1, 1, 2, 3]
      right = [1, 3, 4]
      left & right
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{
			name: "common elements only",
			fn:   "basic",
			want: NewArray([]Value{NewInt(2), NewInt(3)}),
		},
		{
			name: "duplicates removed",
			fn:   "removes_duplicates",
			want: NewArray([]Value{NewInt(1), NewInt(3)}),
		},
		{
			name: "left order preserved",
			fn:   "preserves_left_order",
			want: NewArray([]Value{NewInt(3), NewInt(1), NewInt(2)}),
		},
		{
			name: "disjoint arrays yield empty",
			fn:   "disjoint",
			want: NewArray([]Value{}),
		},
		{
			name: "empty left yields empty",
			fn:   "empty_left",
			want: NewArray([]Value{}),
		},
		{
			name: "empty right yields empty",
			fn:   "empty_right",
			want: NewArray([]Value{}),
		},
		{
			name: "mixed scalar types compare by value",
			fn:   "mixed_types",
			want: NewArray([]Value{NewString("a"), NewSymbol("b")}),
		},
		{
			name: "nested arrays compare by deep equality",
			fn:   "nested_values",
			want: NewArray([]Value{
				NewArray([]Value{NewInt(2)}),
				NewArray([]Value{NewInt(3)}),
			}),
		},
		{
			name: "locals intersect",
			fn:   "locals",
			want: NewArray([]Value{NewInt(1), NewInt(3)}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, nil)
			if diff := valueDiff(tc.want, got); diff != "" {
				t.Fatalf("intersection mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestArrayIntersectionSpacingShapes locks in end-to-end evaluation of the
// intersection operator across the spacing shapes the parser must read as the
// binary operator: flush on both sides ("left&right") and a trailing "&" line
// continuation. Only the detached-but-flush "left &right" shape is a block-pass.
func TestArrayIntersectionSpacingShapes(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def flush_both_sides()
      left = [1, 2, 3]
      right = [2, 3, 4]
      left&right
    end

    def trailing_continuation()
      left = [1, 2, 3]
      right = [2, 3, 4]
      left &
        right
    end
    `)

	tests := []struct {
		name string
		fn   string
	}{
		{name: "flush both sides", fn: "flush_both_sides"},
		{name: "trailing continuation", fn: "trailing_continuation"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, nil)
			want := NewArray([]Value{NewInt(2), NewInt(3)})
			if diff := valueDiff(want, got); diff != "" {
				t.Fatalf("intersection mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCollectionOperatorErrors(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def shovel_non_array()
      5 << 3
    end

    def intersect_non_array_left()
      5 & [1]
    end

    def intersect_non_array_right()
      [1] & 5
    end
    `)

	tests := []struct {
		name string
		fn   string
		want string
	}{
		{
			name: "shovel onto non-array",
			fn:   "shovel_non_array",
			want: "unsupported shovel operands",
		},
		{
			name: "intersection with non-array left",
			fn:   "intersect_non_array_left",
			want: "unsupported intersection operands",
		},
		{
			name: "intersection with non-array right",
			fn:   "intersect_non_array_right",
			want: "unsupported intersection operands",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tc.fn, nil, CallOptions{}, tc.want)
		})
	}
}

// TestCollectionOperatorPrecedence verifies the runtime evaluates the operators
// with the parsed precedence: "+" tighter than "<<" tighter than "&".
func TestCollectionOperatorPrecedence(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def plus_then_shovel()
      [1] + [2] << 3
    end

    def shovel_then_intersect()
      [1, 2] << 3 & [3]
    end
    `)

	compareArrays(t, callFunc(t, script, "plus_then_shovel", nil),
		[]Value{NewInt(1), NewInt(2), NewInt(3)})
	compareArrays(t, callFunc(t, script, "shovel_then_intersect", nil),
		[]Value{NewInt(3)})
}
