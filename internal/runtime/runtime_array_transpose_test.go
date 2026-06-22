package runtime

import (
	"context"
	"testing"
)

func TestArrayTranspose(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def rectangular()
      [[1, 2], [3, 4]].transpose
    end

    def empty()
      [].transpose
    end

    def single_row()
      [[1, 2, 3]].transpose
    end

    def single_column()
      [[1], [2], [3]].transpose
    end

    def empty_rows()
      [[], []].transpose
    end

    def nested_values()
      [[[1], 2], [[3], 4]].transpose
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{
			name: "rectangular swaps rows and columns",
			fn:   "rectangular",
			want: NewArray([]Value{
				NewArray([]Value{NewInt(1), NewInt(3)}),
				NewArray([]Value{NewInt(2), NewInt(4)}),
			}),
		},
		{
			name: "empty input yields empty output",
			fn:   "empty",
			want: NewArray([]Value{}),
		},
		{
			name: "single row becomes a column of singletons",
			fn:   "single_row",
			want: NewArray([]Value{
				NewArray([]Value{NewInt(1)}),
				NewArray([]Value{NewInt(2)}),
				NewArray([]Value{NewInt(3)}),
			}),
		},
		{
			name: "single column collapses into a single row",
			fn:   "single_column",
			want: NewArray([]Value{
				NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
			}),
		},
		{
			name: "rows of zero length produce no columns",
			fn:   "empty_rows",
			want: NewArray([]Value{}),
		},
		{
			name: "nested element values are preserved by reference position",
			fn:   "nested_values",
			want: NewArray([]Value{
				NewArray([]Value{NewArray([]Value{NewInt(1)}), NewArray([]Value{NewInt(3)})}),
				NewArray([]Value{NewInt(2), NewInt(4)}),
			}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, nil)
			if diff := valueDiff(tc.want, got); diff != "" {
				t.Fatalf("transpose mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestArrayTransposeRejectsMisuse(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def with_argument()
      [[1]].transpose(2)
    end

    def ragged()
      [[1, 2], [3]].transpose
    end

    def first_not_array()
      [1, 2].transpose
    end

    def later_not_array()
      [[1, 2], 3].transpose
    end
    `)

	tests := []struct {
		name string
		fn   string
		want string
	}{
		{
			name: "arguments are rejected",
			fn:   "with_argument",
			want: "array.transpose does not take arguments",
		},
		{
			name: "ragged rows report the offending index and lengths",
			fn:   "ragged",
			want: "array.transpose requires equal-length rows, but element at index 1 has length 1 (expected 2)",
		},
		{
			name: "leading non-array element reports its type",
			fn:   "first_not_array",
			want: "array.transpose requires arrays as elements, but element at index 0 is a int",
		},
		{
			name: "trailing non-array element reports its index and type",
			fn:   "later_not_array",
			want: "array.transpose requires arrays as elements, but element at index 1 is a int",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tc.fn, nil, CallOptions{}, tc.want)
		})
	}
}

func TestArrayTransposeHonorsMemoryQuota(t *testing.T) {
	t.Parallel()

	// A wide, short matrix maximizes header growth: 2 rows of n columns
	// transpose into n freshly allocated single-pair rows, so the result
	// dominates the input's two row headers.
	const columns = 4000
	rowA := make([]Value, columns)
	rowB := make([]Value, columns)
	for i := range columns {
		rowA[i] = NewInt(int64(i))
		rowB[i] = NewInt(int64(i + 1))
	}
	matrix := NewArray([]Value{NewArray(rowA), NewArray(rowB)})

	inputBytes := newMemoryEstimator().value(matrix)
	// Sized to admit the input but reject the transposed result.
	quota := inputBytes + inputBytes/2

	cfg := Config{StepQuota: 1_000_000, MemoryQuotaBytes: quota}

	// The input alone must fit, proving the quota trips on the transpose
	// result rather than on argument binding.
	fits := compileScriptWithConfig(t, cfg, `def run(m); m.size; end`)
	if _, err := fits.Call(context.Background(), "run", []Value{matrix}, CallOptions{}); err != nil {
		t.Fatalf("input matrix should fit under quota %d: %v", quota, err)
	}

	transposes := compileScriptWithConfig(t, cfg, `def run(m); m.transpose; end`)
	requireCallRuntimeErrorType(t, transposes, "run", []Value{matrix}, CallOptions{}, runtimeErrorTypeLimit)
}
