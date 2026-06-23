package runtime

import (
	"context"
	"errors"
	"testing"
)

// TestArrayFillValueForms exercises the explicit-value forms of Array#fill,
// covering whole-array fills, start/length slices, range selection, negative
// starts and bounds, out-of-range starts, growth past the receiver, and empty
// receivers, comparing each result against the Ruby Array#fill baseline.
func TestArrayFillValueForms(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def fill_all(values, value)
      values.fill(value)
    end

    def fill_start(values, value, start)
      values.fill(value, start)
    end

    def fill_start_length(values, value, start, length)
      values.fill(value, start, length)
    end

    def fill_range(values, value, range)
      values.fill(value, range)
    end
    `)

	makeArr := func(ints ...int64) Value {
		out := make([]Value, len(ints))
		for i, n := range ints {
			out[i] = NewInt(n)
		}
		return NewArray(out)
	}

	tests := []struct {
		name string
		fn   string
		args []Value
		want []Value
	}{
		{
			name: "whole array",
			fn:   "fill_all",
			args: []Value{makeArr(1, 2, 3), NewInt(0)},
			want: []Value{NewInt(0), NewInt(0), NewInt(0)},
		},
		{
			name: "empty array",
			fn:   "fill_all",
			args: []Value{NewArray([]Value{}), NewInt(0)},
			want: []Value{},
		},
		{
			name: "from start to end",
			fn:   "fill_start",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(1)},
			want: []Value{NewInt(1), NewInt(0), NewInt(0)},
		},
		{
			name: "start and length",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(1), NewInt(2)},
			want: []Value{NewInt(1), NewInt(0), NewInt(0)},
		},
		{
			name: "zero length is a no-op",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(1), NewInt(0)},
			want: []Value{NewInt(1), NewInt(2), NewInt(3)},
		},
		{
			name: "negative length is a no-op",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(1), NewInt(-1)},
			want: []Value{NewInt(1), NewInt(2), NewInt(3)},
		},
		{
			// Ruby: [1, 2, 3].fill(0, 5, 0) => [1, 2, 3, nil, nil]. An explicit
			// zero length still grows the array up to the start, padding the gap
			// with nil even though the fill window itself is empty.
			name: "zero length past end grows and pads with nil",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(5), NewInt(0)},
			want: []Value{NewInt(1), NewInt(2), NewInt(3), NewNil(), NewNil()},
		},
		{
			// Ruby: [1, 2, 3].fill(0, 5, -3) => [1, 2, 3]. A negative length is a
			// pure no-op and never grows the array, even when the start is past
			// the end, distinguishing it from the zero-length case above.
			name: "negative length past end is a no-op",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(5), NewInt(-3)},
			want: []Value{NewInt(1), NewInt(2), NewInt(3)},
		},
		{
			// Ruby: [1, 2, 3].fill(0, 3, 0) => [1, 2, 3]. A zero length whose
			// start sits exactly at the end neither grows nor changes the array.
			name: "zero length at end is a no-op",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(3), NewInt(0)},
			want: []Value{NewInt(1), NewInt(2), NewInt(3)},
		},
		{
			name: "negative start counts from end",
			fn:   "fill_start",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(-1)},
			want: []Value{NewInt(1), NewInt(2), NewInt(0)},
		},
		{
			name: "negative start beyond length clamps to zero",
			fn:   "fill_start",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(-5)},
			want: []Value{NewInt(0), NewInt(0), NewInt(0)},
		},
		{
			name: "negative start beyond length with length clamps to zero",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(-10), NewInt(1)},
			want: []Value{NewInt(0), NewInt(2), NewInt(3)},
		},
		{
			name: "start past end without length is a no-op",
			fn:   "fill_start",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(5)},
			want: []Value{NewInt(1), NewInt(2), NewInt(3)},
		},
		{
			name: "length grows the array",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(1), NewInt(5)},
			want: []Value{NewInt(1), NewInt(0), NewInt(0), NewInt(0), NewInt(0), NewInt(0)},
		},
		{
			name: "start past end with length pads with nil",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(5), NewInt(2)},
			want: []Value{NewInt(1), NewInt(2), NewInt(3), NewNil(), NewNil(), NewInt(0), NewInt(0)},
		},
		{
			name: "start equal to length appends",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(3), NewInt(2)},
			want: []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(0), NewInt(0)},
		},
		{
			name: "inclusive range",
			fn:   "fill_range",
			args: []Value{makeArr(1, 2, 3), NewString("x"), NewRange(Range{Start: 1, End: 2})},
			want: []Value{NewInt(1), NewString("x"), NewString("x")},
		},
		{
			name: "exclusive range",
			fn:   "fill_range",
			args: []Value{makeArr(1, 2, 3), NewString("x"), NewRange(Range{Start: 1, End: 3, Exclusive: true})},
			want: []Value{NewInt(1), NewString("x"), NewString("x")},
		},
		{
			name: "exclusive range covering all",
			fn:   "fill_range",
			args: []Value{makeArr(1, 2, 3), NewString("x"), NewRange(Range{Start: 0, End: 3, Exclusive: true})},
			want: []Value{NewString("x"), NewString("x"), NewString("x")},
		},
		{
			name: "negative range bounds",
			fn:   "fill_range",
			args: []Value{makeArr(1, 2, 3, 4, 5), NewString("x"), NewRange(Range{Start: -3, End: -2})},
			want: []Value{NewInt(1), NewInt(2), NewString("x"), NewString("x"), NewInt(5)},
		},
		{
			name: "range begin greater than end is a no-op",
			fn:   "fill_range",
			args: []Value{makeArr(1, 2, 3, 4, 5), NewString("x"), NewRange(Range{Start: 3, End: 1})},
			want: []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4), NewInt(5)},
		},
		{
			name: "range end past length grows the array",
			fn:   "fill_range",
			args: []Value{makeArr(1, 2, 3), NewString("x"), NewRange(Range{Start: 1, End: 6})},
			want: []Value{NewInt(1), NewString("x"), NewString("x"), NewString("x"), NewString("x"), NewString("x"), NewString("x")},
		},
		{
			name: "range start past length pads with nil",
			fn:   "fill_range",
			args: []Value{makeArr(1, 2, 3), NewString("x"), NewRange(Range{Start: 5, End: 6})},
			want: []Value{NewInt(1), NewInt(2), NewInt(3), NewNil(), NewNil(), NewString("x"), NewString("x")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			compareArrays(t, callFunc(t, script, tc.fn, tc.args), tc.want)
		})
	}
}

// TestArrayFillBlockForms exercises the block forms of Array#fill, where the
// block receives each destination index and there is no explicit value
// argument. It mirrors the value-form coverage for whole-array, start, length,
// range, and growth windows.
func TestArrayFillBlockForms(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def fill_all(values)
      values.fill do |i|
        i * 10
      end
    end

    def fill_start(values, start)
      values.fill(start) do |i|
        i * 10
      end
    end

    def fill_start_length(values, start, length)
      values.fill(start, length) do |i|
        i * 10
      end
    end

    def fill_range(values, range)
      values.fill(range) do |i|
        i * 10
      end
    end
    `)

	makeArr := func(ints ...int64) Value {
		out := make([]Value, len(ints))
		for i, n := range ints {
			out[i] = NewInt(n)
		}
		return NewArray(out)
	}

	tests := []struct {
		name string
		fn   string
		args []Value
		want []Value
	}{
		{
			name: "whole array",
			fn:   "fill_all",
			args: []Value{makeArr(1, 2, 3)},
			want: []Value{NewInt(0), NewInt(10), NewInt(20)},
		},
		{
			name: "empty array",
			fn:   "fill_all",
			args: []Value{NewArray([]Value{})},
			want: []Value{},
		},
		{
			name: "from start to end",
			fn:   "fill_start",
			args: []Value{makeArr(1, 2, 3, 4, 5), NewInt(2)},
			want: []Value{NewInt(1), NewInt(2), NewInt(20), NewInt(30), NewInt(40)},
		},
		{
			name: "start and length",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3, 4, 5), NewInt(1), NewInt(2)},
			want: []Value{NewInt(1), NewInt(10), NewInt(20), NewInt(4), NewInt(5)},
		},
		{
			name: "range",
			fn:   "fill_range",
			args: []Value{makeArr(1, 2, 3, 4, 5), NewRange(Range{Start: 1, End: 3})},
			want: []Value{NewInt(1), NewInt(10), NewInt(20), NewInt(30), NewInt(5)},
		},
		{
			name: "start past end without length is a no-op",
			fn:   "fill_start",
			args: []Value{makeArr(1, 2, 3), NewInt(5)},
			want: []Value{NewInt(1), NewInt(2), NewInt(3)},
		},
		{
			name: "length grows the array using the new indexes",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(1), NewInt(5)},
			want: []Value{NewInt(1), NewInt(10), NewInt(20), NewInt(30), NewInt(40), NewInt(50)},
		},
		{
			name: "start past end with length pads with nil",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(5), NewInt(2)},
			want: []Value{NewInt(1), NewInt(2), NewInt(3), NewNil(), NewNil(), NewInt(50), NewInt(60)},
		},
		{
			// Block form mirrors the value form: an explicit zero length past the
			// end grows the array up to the start and pads the gap with nil,
			// while the empty window never invokes the block.
			name: "zero length past end grows and pads with nil",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(5), NewInt(0)},
			want: []Value{NewInt(1), NewInt(2), NewInt(3), NewNil(), NewNil()},
		},
		{
			// A negative length is a pure no-op for the block form too.
			name: "negative length past end is a no-op",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(5), NewInt(-3)},
			want: []Value{NewInt(1), NewInt(2), NewInt(3)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			compareArrays(t, callFunc(t, script, tc.fn, tc.args), tc.want)
		})
	}
}

// TestArrayFillStartArgWithBlockIsStartNotValue pins the counterintuitive
// value+block case: when a block is given, a positional argument is the start
// index, never a fill value. A reader of fill(0) { |i| ... } might expect 0 to
// be the fill value with the block ignored, but Ruby (and Vibescript) treats 0
// as the start and fills from there to the end using the block results, so the
// matching value-form result [0, 0, 0] differs from the block-form result
// [0, 10, 20].
func TestArrayFillStartArgWithBlockIsStartNotValue(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def fill_start_block(values, start)
      values.fill(start) do |i|
        i * 10
      end
    end
    `)

	arr := NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})
	got := callFunc(t, script, "fill_start_block", []Value{arr, NewInt(0)})
	compareArrays(t, got, []Value{NewInt(0), NewInt(10), NewInt(20)})
}

// TestArrayFillDoesNotMutateReceiver confirms fill returns a new array and
// leaves the receiver untouched, consistent with the immutable collection
// helpers it sits beside.
func TestArrayFillDoesNotMutateReceiver(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def fill_keeps_original()
      original = [1, 2, 3]
      filled = original.fill(0)
      { original: original, filled: filled }
    end
    `)

	result := callFunc(t, script, "fill_keeps_original", nil).Hash()
	compareArrays(t, result["original"], []Value{NewInt(1), NewInt(2), NewInt(3)})
	compareArrays(t, result["filled"], []Value{NewInt(0), NewInt(0), NewInt(0)})
}

// TestArrayFillArgumentRejection covers the error paths: missing value and
// block, too many selectors, a length paired with a range, non-integer
// start/length, and a range whose negative bound falls before the array start.
func TestArrayFillArgumentRejection(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def no_value_no_block(values)
      values.fill
    end

    def too_many_args(values)
      values.fill(0, 1, 2, 3)
    end

    def length_with_range(values)
      values.fill(0, 1..2, 5)
    end

    def bad_start(values)
      values.fill(0, "x")
    end

    def bad_length(values)
      values.fill(0, 1, "x")
    end

    def range_out_of_range(values)
      values.fill("x", -5..-1)
    end
    `)

	arr := NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})

	tests := []struct {
		name string
		fn   string
		want string
	}{
		{"no value no block", "no_value_no_block", "array.fill requires a value or a block"},
		{"too many args", "too_many_args", "array.fill accepts at most a start and length"},
		{"length with range", "length_with_range", "array.fill does not accept a length with a range"},
		{"bad start", "bad_start", "array.fill start must be integer"},
		{"bad length", "bad_length", "array.fill length must be integer"},
		{"range out of range", "range_out_of_range", "array.fill range -5..-1 out of range"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tc.fn, []Value{arr}, CallOptions{}, tc.want)
		})
	}
}

// TestArrayFillRejectsHugeWindow confirms an oversized window (one whose end
// overflows the native int range) is rejected up front rather than wrapping
// into a silent no-op or panicking on allocation.
func TestArrayFillRejectsHugeWindow(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def fill_huge_length(values)
      values.fill(0, 9223372036854775807, 9223372036854775807)
    end

    def fill_huge_inclusive_range(values)
      values.fill("x", 1..9223372036854775807)
    end
    `)

	arr := NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})

	requireCallErrorContains(t, script, "fill_huge_length", []Value{arr}, CallOptions{}, "array.fill window is too large")
	requireCallErrorContains(t, script, "fill_huge_inclusive_range", []Value{arr}, CallOptions{}, "array.fill window is too large")
}

// TestArrayFillMemoryQuota confirms a growth that would exceed the memory quota
// trips the sandbox limit up front instead of reserving a huge backing array.
func TestArrayFillMemoryQuota(t *testing.T) {
	t.Parallel()
	source := `def run()
  [1, 2, 3].fill(0, 0, 9000000000000000000)
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 * 1024}, source)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

// TestArrayFillStepQuota confirms each filled element consumes a step, so a
// growth larger than the step quota stops on the step limit even when the
// memory quota is generous.
func TestArrayFillStepQuota(t *testing.T) {
	t.Parallel()
	source := `def run()
  [].fill(0, 0, 1000000)
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: 64 << 20}, source)
	requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
}

// TestArrayFillContextCancellation confirms a canceled context aborts the fill:
// step() polls cancellation on its first invocation, so even a tiny array is
// enough to observe it.
func TestArrayFillContextCancellation(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      [1, 2, 3].fill(0)
    end
    `)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("fill under canceled context = %v, want context.Canceled", err)
	}
}
