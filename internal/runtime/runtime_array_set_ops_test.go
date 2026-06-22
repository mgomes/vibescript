package runtime

import (
	"context"
	"testing"
)

func TestArrayUnion(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def two()
      [1, 2].union([2, 3])
    end

    def many()
      [1, 2].union([2, 3], [3, 4])
    end

    def no_args()
      [1, 1, 2, 3].union
    end

    def collapses_receiver_duplicates()
      [1, 1, 2].union([2, 2, 3])
    end

    def empty_others()
      [1, 2].union([], [])
    end

    def mixed_types()
      [1, "a", :b].union(["a", 2, :b])
    end

    def nested_values()
      [[1], [2]].union([[2], [3]])
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{
			name: "two arrays remove shared elements",
			fn:   "two",
			want: NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		},
		{
			name: "multiple arrays union in order",
			fn:   "many",
			want: NewArray([]Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)}),
		},
		{
			name: "no arguments deduplicates the receiver",
			fn:   "no_args",
			want: NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		},
		{
			name: "receiver duplicates collapse",
			fn:   "collapses_receiver_duplicates",
			want: NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		},
		{
			name: "empty other arrays still deduplicate",
			fn:   "empty_others",
			want: NewArray([]Value{NewInt(1), NewInt(2)}),
		},
		{
			name: "mixed scalar types compare by value",
			fn:   "mixed_types",
			want: NewArray([]Value{NewInt(1), NewString("a"), NewSymbol("b"), NewInt(2)}),
		},
		{
			name: "nested arrays compare by deep equality",
			fn:   "nested_values",
			want: NewArray([]Value{
				NewArray([]Value{NewInt(1)}),
				NewArray([]Value{NewInt(2)}),
				NewArray([]Value{NewInt(3)}),
			}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, nil)
			if diff := valueDiff(tc.want, got); diff != "" {
				t.Fatalf("union mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestArrayDifference(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def single()
      [1, 2, 2, 3].difference([2])
    end

    def many()
      [1, 2, 3, 4].difference([2], [4])
    end

    def preserves_receiver_duplicates()
      [1, 1, 2, 3].difference([2])
    end

    def no_args()
      [1, 1, 2, 3].difference
    end

    def empty_others()
      [1, 2, 2].difference([], [])
    end

    def removes_all()
      [1, 2, 3].difference([1, 2, 3])
    end

    def nested_values()
      [[1], [2], [3]].difference([[2]])
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{
			name: "single argument removes matching elements",
			fn:   "single",
			want: NewArray([]Value{NewInt(1), NewInt(3)}),
		},
		{
			name: "multiple arguments remove from any",
			fn:   "many",
			want: NewArray([]Value{NewInt(1), NewInt(3)}),
		},
		{
			name: "receiver duplicates that survive are kept",
			fn:   "preserves_receiver_duplicates",
			want: NewArray([]Value{NewInt(1), NewInt(1), NewInt(3)}),
		},
		{
			name: "no arguments returns the receiver unchanged",
			fn:   "no_args",
			want: NewArray([]Value{NewInt(1), NewInt(1), NewInt(2), NewInt(3)}),
		},
		{
			name: "empty other arrays leave the receiver intact",
			fn:   "empty_others",
			want: NewArray([]Value{NewInt(1), NewInt(2), NewInt(2)}),
		},
		{
			name: "removing every element yields an empty array",
			fn:   "removes_all",
			want: NewArray([]Value{}),
		},
		{
			name: "nested arrays compare by deep equality",
			fn:   "nested_values",
			want: NewArray([]Value{
				NewArray([]Value{NewInt(1)}),
				NewArray([]Value{NewInt(3)}),
			}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, nil)
			if diff := valueDiff(tc.want, got); diff != "" {
				t.Fatalf("difference mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestArraySetOpsDoNotMutateReceiver(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def union_keeps_source()
      a = [1, 2]
      a.union([2, 3])
      a
    end

    def difference_keeps_source()
      a = [1, 2, 3]
      a.difference([2])
      a
    end
    `)

	wantUnion := NewArray([]Value{NewInt(1), NewInt(2)})
	if diff := valueDiff(wantUnion, callFunc(t, script, "union_keeps_source", nil)); diff != "" {
		t.Fatalf("union mutated receiver (-want +got):\n%s", diff)
	}

	wantDifference := NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})
	if diff := valueDiff(wantDifference, callFunc(t, script, "difference_keeps_source", nil)); diff != "" {
		t.Fatalf("difference mutated receiver (-want +got):\n%s", diff)
	}
}

func TestArraySetOpsRejectNonArrayArguments(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def union_scalar()
      [1, 2].union(3)
    end

    def union_later_scalar()
      [1, 2].union([3], 4)
    end

    def difference_scalar()
      [1, 2].difference(3)
    end

    def difference_later_scalar()
      [1, 2].difference([3], 4)
    end
    `)

	tests := []struct {
		name string
		fn   string
		want string
	}{
		{
			name: "union rejects a scalar argument",
			fn:   "union_scalar",
			want: "array.union arguments must be arrays",
		},
		{
			name: "union rejects a later scalar argument",
			fn:   "union_later_scalar",
			want: "array.union arguments must be arrays",
		},
		{
			name: "difference rejects a scalar argument",
			fn:   "difference_scalar",
			want: "array.difference arguments must be arrays",
		},
		{
			name: "difference rejects a later scalar argument",
			fn:   "difference_later_scalar",
			want: "array.difference arguments must be arrays",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tc.fn, nil, CallOptions{}, tc.want)
		})
	}
}

func TestArraySetOpsRejectKeywordArguments(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def union_keyword()
      [1, 2].union(other: [2, 3])
    end

    def union_keyword_with_array()
      [1, 2].union([2, 3], other: [4])
    end

    def difference_keyword()
      [1, 2].difference(other: [2])
    end

    def difference_keyword_with_array()
      [1, 2].difference([2], other: [1])
    end
    `)

	tests := []struct {
		name string
		fn   string
		want string
	}{
		{
			name: "union rejects a keyword argument",
			fn:   "union_keyword",
			want: "array.union does not take keyword arguments",
		},
		{
			name: "union rejects a keyword alongside an array",
			fn:   "union_keyword_with_array",
			want: "array.union does not take keyword arguments",
		},
		{
			name: "difference rejects a keyword argument",
			fn:   "difference_keyword",
			want: "array.difference does not take keyword arguments",
		},
		{
			name: "difference rejects a keyword alongside an array",
			fn:   "difference_keyword_with_array",
			want: "array.difference does not take keyword arguments",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tc.fn, nil, CallOptions{}, tc.want)
		})
	}
}

func TestArrayUnionHonorsMemoryQuota(t *testing.T) {
	t.Parallel()

	// Two disjoint arrays union into a result roughly the size of both inputs
	// combined. Sizing the quota to admit both bound inputs with a slim margin
	// forces the limit to trip on the freshly materialized union result rather
	// than on argument binding.
	const count = 4000
	left := make([]Value, count)
	right := make([]Value, count)
	for i := range count {
		left[i] = NewInt(int64(i))
		right[i] = NewInt(int64(i + count))
	}
	leftArr := NewArray(left)
	rightArr := NewArray(right)

	inputBytes := newMemoryEstimator().value(leftArr) + newMemoryEstimator().value(rightArr)
	quota := inputBytes + inputBytes/4

	cfg := Config{StepQuota: 1_000_000, MemoryQuotaBytes: quota}

	fits := compileScriptWithConfig(t, cfg, `def run(a, b); a.size + b.size; end`)
	if _, err := fits.Call(context.Background(), "run", []Value{leftArr, rightArr}, CallOptions{}); err != nil {
		t.Fatalf("inputs should fit under quota %d: %v", quota, err)
	}

	unions := compileScriptWithConfig(t, cfg, `def run(a, b); a.union(b); end`)
	requireCallRuntimeErrorType(t, unions, "run", []Value{leftArr, rightArr}, CallOptions{}, runtimeErrorTypeLimit)
}

func TestArrayDifferenceHonorsMemoryQuota(t *testing.T) {
	t.Parallel()

	// difference returns a copy of the surviving receiver elements, so a quota
	// that fits the input plus a slim margin still rejects the freshly allocated
	// result when nothing is removed.
	const count = 8000
	left := make([]Value, count)
	for i := range count {
		left[i] = NewInt(int64(i))
	}
	leftArr := NewArray(left)
	empty := NewArray([]Value{})

	inputBytes := newMemoryEstimator().value(leftArr)
	quota := inputBytes + inputBytes/4

	cfg := Config{StepQuota: 1_000_000, MemoryQuotaBytes: quota}

	fits := compileScriptWithConfig(t, cfg, `def run(a, b); a.size + b.size; end`)
	if _, err := fits.Call(context.Background(), "run", []Value{leftArr, empty}, CallOptions{}); err != nil {
		t.Fatalf("input should fit under quota %d: %v", quota, err)
	}

	differences := compileScriptWithConfig(t, cfg, `def run(a, b); a.difference(b); end`)
	requireCallRuntimeErrorType(t, differences, "run", []Value{leftArr, empty}, CallOptions{}, runtimeErrorTypeLimit)
}
