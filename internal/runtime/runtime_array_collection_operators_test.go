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

// TestArrayShovelIsNonMutating documents the Vibescript-specific divergence
// from Ruby: a bare "values << x" expression statement produces a new array and
// leaves the receiver unchanged, because the language's collections are
// immutable. Observable accumulation requires reassignment.
func TestArrayShovelIsNonMutating(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def discarded()
      values = [1, 2]
      values << 3
      values
    end
    `)

	got := callFunc(t, script, "discarded", nil)
	want := NewArray([]Value{NewInt(1), NewInt(2)})
	if diff := valueDiff(want, got); diff != "" {
		t.Fatalf("shovel mismatch (-want +got):\n%s", diff)
	}
}

// TestArrayShovelAccumulatorPreservesAliasIsolation mirrors the push/concat
// accumulator alias tests: the reassignment fast path reuses a hidden backing
// buffer, but an escaped alias taken before an append must never observe later
// appends.
func TestArrayShovelAccumulatorPreservesAliasIsolation(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def accumulate(n)
      out = []
      for i in 1..n
        out = out << i
      end
      out
    end

    def alias_isolation()
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

	aliased := callFunc(t, script, "alias_isolation", nil).Hash()
	compareArrays(t, aliased["a"], []Value{NewInt(1), NewInt(2)})
	compareArrays(t, aliased["b"], []Value{NewInt(9)})

	repeated := callFunc(t, script, "repeated_alias", nil).Hash()
	compareArrays(t, repeated["a"], []Value{NewInt(1), NewInt(2)})
	compareArrays(t, repeated["b"], []Value{NewInt(1), NewInt(3)})
}

// TestArrayShovelAccumulatorDetachesEscapedBlockResults mirrors the push fast
// path's escape test: each array the block returns must be an independent
// snapshot rather than an alias of the shared backing buffer, so mutating one
// escaped result never disturbs the others.
func TestArrayShovelAccumulatorDetachesEscapedBlockResults(t *testing.T) {
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
	compareArrays(t, results[0], []Value{NewInt(1)})
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
