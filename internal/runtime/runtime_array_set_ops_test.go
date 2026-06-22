package runtime

import (
	"context"
	"math"
	goruntime "runtime"
	"testing"
	"time"
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

// The set helpers must build their result while iterating the inputs rather
// than first materializing a concatenated or flattened intermediate slice. Such
// a transient escapes the post-call memory check, so a quota sized for the
// receiver plus the result could still be exceeded mid-call. The two tests
// below feed inputs whose distinct elements (and thus the result and membership
// set) are tiny while the total input length is large, isolating the transient
// slice as the only allocation that would scale with the inputs.
const (
	setOpDistinct  = 64
	setOpInputLen  = 600_000
	setOpInputArgs = 2
)

func setOpRepeatingInput() []Value {
	values := make([]Value, setOpInputLen)
	for i := range values {
		values[i] = NewInt(int64(i % setOpDistinct))
	}
	return values
}

func TestUnionArrayValuesAvoidsTransientConcatenation(t *testing.T) {
	// Not parallel: this test reads process-wide allocation counters and must
	// not race other goroutines' allocations.

	// Concatenating left with every other before deduping peaks at
	// len(left)+sum(others) Values. With heavily repeating inputs the result and
	// membership set stay tiny, so that temporary slice dominates allocation.
	input := setOpRepeatingInput()
	others := make([][]Value, setOpInputArgs)
	for i := range others {
		others[i] = input
	}

	want := uniqueValues(input)

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	got := unionArrayValues(input, others)
	goruntime.ReadMemStats(&after)

	if diff := valueDiff(NewArray(want), NewArray(got)); diff != "" {
		t.Fatalf("union mismatch (-want +got):\n%s", diff)
	}

	// The concatenation transient would reserve (1+args) copies of the input.
	// Bound the allocation well under that so reintroducing it fails loudly while
	// staying robust against unrelated allocation noise.
	transientBytes := uint64(setOpInputLen) * uint64(1+setOpInputArgs) * uint64(estimatedValueBytes)
	ceiling := transientBytes / 4
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > ceiling {
		t.Fatalf("union allocated %d bytes, want <= %d (concatenation transient would be %d)",
			allocated, ceiling, transientBytes)
	}
}

func TestDifferenceArrayValuesAvoidsTransientFlattening(t *testing.T) {
	// Not parallel: this test reads process-wide allocation counters and must
	// not race other goroutines' allocations.

	// Flattening every other into a single removal slice peaks at sum(others)
	// Values. With a small receiver and heavily repeating arguments the result
	// and membership set stay tiny, so that flattened copy dominates allocation.
	arg := setOpRepeatingInput()
	others := make([][]Value, setOpInputArgs)
	for i := range others {
		others[i] = arg
	}
	left := []Value{NewInt(int64(setOpDistinct))}

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	got := differenceArrayValues(left, others)
	goruntime.ReadMemStats(&after)

	want := NewArray([]Value{NewInt(int64(setOpDistinct))})
	if diff := valueDiff(want, NewArray(got)); diff != "" {
		t.Fatalf("difference mismatch (-want +got):\n%s", diff)
	}

	// The flattening transient would reserve sum(others) Values. Bound the
	// allocation well under that so reintroducing it fails loudly.
	flattenedBytes := uint64(setOpInputLen) * uint64(setOpInputArgs) * uint64(estimatedValueBytes)
	ceiling := flattenedBytes / 4
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > ceiling {
		t.Fatalf("difference allocated %d bytes, want <= %d (flattening transient would be %d)",
			allocated, ceiling, flattenedBytes)
	}
}

// distinctCompositeValues returns n single-element arrays, each holding a
// different integer, so every value is a distinct composite that must be
// compared with Value.Equal rather than indexed as a scalar.
func distinctCompositeValues(n int) []Value {
	values := make([]Value, n)
	for i := range values {
		values[i] = NewArray([]Value{NewInt(int64(i))})
	}
	return values
}

// measureDifferenceRemoval times differenceArrayValues against a removal side of
// n distinct composites, taking the fastest of a few runs so transient
// scheduler noise cannot inflate the reading. The receiver is a single value
// absent from the removal side, isolating the cost of building the removal
// membership structure.
func measureDifferenceRemoval(n int) time.Duration {
	left := []Value{NewArray([]Value{NewInt(-1)})}
	others := [][]Value{distinctCompositeValues(n)}
	best := time.Duration(math.MaxInt64)
	for range 5 {
		start := time.Now()
		_ = differenceArrayValues(left, others)
		if elapsed := time.Since(start); elapsed < best {
			best = elapsed
		}
	}
	return best
}

// TestDifferenceArrayValuesRemovalIsNotQuadratic guards against deduplicating
// the difference removal side. A deduping set scans every previously inserted
// composite via Value.Equal on each insert, so building the removal structure
// from m distinct composites costs O(m^2); a membership-only structure that
// appends composites without scanning keeps it O(m). Doubling the removal side
// must therefore roughly double the work, not quadruple it.
func TestDifferenceArrayValuesRemovalIsNotQuadratic(t *testing.T) {
	// Not parallel: timing measurements must not contend with other tests.

	const base = 20_000

	// Warm up so first-touch allocation and code caching do not skew the first
	// measured size.
	measureDifferenceRemoval(base)

	single := measureDifferenceRemoval(base)
	double := measureDifferenceRemoval(2 * base)

	// Linear scaling lands near 2x; quadratic scaling lands near 4x. A ceiling of
	// 3x sits clearly between the two, failing loudly on a quadratic regression
	// while tolerating ordinary timing noise. Guard against a degenerate
	// near-zero baseline that would make the ratio meaningless.
	const maxRatio = 3.0
	if single <= 0 {
		t.Fatalf("baseline measurement was non-positive: %v", single)
	}
	if ratio := float64(double) / float64(single); ratio > maxRatio {
		t.Fatalf("difference removal scaled %.2fx from %d to %d composites (single=%v double=%v); want <= %.1fx, quadratic insertion is ~4x",
			ratio, base, 2*base, single, double, maxRatio)
	}
}
