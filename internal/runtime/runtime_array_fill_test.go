package runtime

import (
	"context"
	"errors"
	goruntime "runtime"
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

// TestArrayFillNilSelectors confirms a nil start or nil length is read as
// omitted, matching Ruby's Array#fill: a nil start means 0 and a nil length
// means "to the end". This covers code that forwards optional selectors stored
// in variables defaulting to nil, in both the value and block forms.
func TestArrayFillNilSelectors(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def fill_start(values, value, start)
      values.fill(value, start)
    end

    def fill_start_length(values, value, start, length)
      values.fill(value, start, length)
    end

    def fill_block_start(values, start)
      values.fill(start) do |i|
        i * 10
      end
    end

    def fill_block_start_length(values, start, length)
      values.fill(start, length) do |i|
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
			// Ruby: [1, 2, 3].fill(0, nil) => [0, 0, 0]. A nil start is read as 0.
			name: "nil start value form",
			fn:   "fill_start",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewNil()},
			want: []Value{NewInt(0), NewInt(0), NewInt(0)},
		},
		{
			// Ruby: [1, 2, 3].fill(0, 1, nil) => [1, 0, 0]. A nil length fills to
			// the end starting at the given start.
			name: "nil length value form",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewInt(1), NewNil()},
			want: []Value{NewInt(1), NewInt(0), NewInt(0)},
		},
		{
			// Ruby: [1, 2, 3].fill(0, nil, nil) => [0, 0, 0]. Both selectors nil
			// means start 0 to the end.
			name: "nil start and nil length value form",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewNil(), NewNil()},
			want: []Value{NewInt(0), NewInt(0), NewInt(0)},
		},
		{
			// Ruby: [1, 2, 3].fill(0, nil, 2) => [0, 0, 3]. A nil start with an
			// explicit length fills length elements from index 0.
			name: "nil start with explicit length value form",
			fn:   "fill_start_length",
			args: []Value{makeArr(1, 2, 3), NewInt(0), NewNil(), NewInt(2)},
			want: []Value{NewInt(0), NewInt(0), NewInt(3)},
		},
		{
			// Ruby: [1, 2, 3, 4, 5].fill(nil) { |i| i * 10 } => [0, 10, 20, 30, 40].
			// A nil start in the block form is read as 0.
			name: "nil start block form",
			fn:   "fill_block_start",
			args: []Value{makeArr(1, 2, 3, 4, 5), NewNil()},
			want: []Value{NewInt(0), NewInt(10), NewInt(20), NewInt(30), NewInt(40)},
		},
		{
			// Ruby: [1, 2, 3, 4, 5].fill(2, nil) { |i| i * 10 } => [1, 2, 20, 30, 40].
			// A nil length in the block form fills from the start to the end.
			name: "nil length block form",
			fn:   "fill_block_start_length",
			args: []Value{makeArr(1, 2, 3, 4, 5), NewInt(2), NewNil()},
			want: []Value{NewInt(1), NewInt(2), NewInt(20), NewInt(30), NewInt(40)},
		},
		{
			// Ruby: [1, 2, 3, 4, 5].fill(nil, nil) { |i| i * 10 } =>
			// [0, 10, 20, 30, 40]. Both selectors nil fills the whole array.
			name: "nil start and nil length block form",
			fn:   "fill_block_start_length",
			args: []Value{makeArr(1, 2, 3, 4, 5), NewNil(), NewNil()},
			want: []Value{NewInt(0), NewInt(10), NewInt(20), NewInt(30), NewInt(40)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			compareArrays(t, callFunc(t, script, tc.fn, tc.args), tc.want)
		})
	}
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

// TestArrayFillBlockResultsCountTowardMemoryQuota confirms the block form charges
// each block result toward the memory quota during construction rather than only
// after fill returns. The window (1024 slots * 32 bytes = 32 KiB) keeps the slot
// array well under the 64 KiB quota, so the slot-only backing check never trips;
// only by counting the appended payloads (each block call allocates a fresh
// ~16 KiB string) does the accumulated growth exceed the quota mid-loop.
func TestArrayFillBlockResultsCountTowardMemoryQuota(t *testing.T) {
	t.Parallel()
	source := `def run()
  [].fill(0, 1024) do |i|
    "".ljust(16384)
  end
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 * 1024}, source)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

// TestArrayFillBlockResultsRejectedDuringConstruction confirms the block form
// stops building as soon as accumulated payloads exceed the memory quota instead
// of materializing the full result and only failing on the post-call check. With
// the quota counted incrementally the fill aborts after a handful of elements, so
// total allocation stays far below the full result; the earlier slot-only check
// would let the entire window of ~16 KiB strings allocate first. The allocation
// ceiling fails loudly if that unbounded-growth path ever returns.
func TestArrayFillBlockResultsRejectedDuringConstruction(t *testing.T) {
	// Not parallel: this test reads process-wide allocation counters and must not
	// race other goroutines' allocations.
	const (
		elements    = 1024
		elementSize = 16384
	)
	source := `def run()
  [].fill(0, 1024) do |i|
    "".ljust(16384)
  end
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 64 * 1024}, source)

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	goruntime.ReadMemStats(&after)

	if got := classifyRuntimeErrorType(err); got != runtimeErrorTypeLimit {
		t.Fatalf("fill block over quota classified as %q (%v), want %q", got, err, runtimeErrorTypeLimit)
	}

	// The incremental check aborts after only the handful of ~16 KiB strings that
	// fit under the 64 KiB quota, so total allocation is a tiny fraction of the
	// full window. The earlier slot-only check counted just the 32-byte slots, so
	// it allowed roughly quota/slotSize elements (~2K) to allocate first, retaining
	// tens of MiB of string payload before tripping. TotalAlloc is monotonic and
	// GC-immune, so the gap between the two paths is unambiguous. Use a generous
	// ceiling that clears interpreter overhead yet sits far below the slot-only
	// path's allocation, failing loudly if unbounded growth ever returns.
	const fullWindowBytes = uint64(elements) * uint64(elementSize)
	const ceiling = fullWindowBytes / 8
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > ceiling {
		t.Fatalf("fill allocated %d bytes, want <= %d (full window would be %d)",
			allocated, ceiling, fullWindowBytes)
	}
}

// TestArrayFillSharedBackingCountedOnce confirms the incremental quota accounting
// charges a value's payload once even when it is filled into many slots. The
// value form stores the same string backing in every slot, so the real added
// memory is one ~16 KiB payload plus the slots, not one payload per slot. A naive
// per-element re-walk that re-counted the shared backing each time would
// false-positive here; filling 512 slots from one ~16 KiB string must fit under a
// quota that comfortably holds a single copy plus the slot array.
func TestArrayFillSharedBackingCountedOnce(t *testing.T) {
	t.Parallel()
	source := `def run(s)
  [].fill(s, 0, 512)
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 128 * 1024}, source)
	big := NewString(string(make([]byte, 16384)))
	result, err := script.Call(context.Background(), "run", []Value{big}, CallOptions{})
	if err != nil {
		t.Fatalf("fill with shared backing = %v, want success (backing counted once)", err)
	}
	if got := len(result.Array()); got != 512 {
		t.Fatalf("len = %d, want 512", got)
	}
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

// TestArrayFillPaddedGapStepQuota confirms a fill whose window is empty but whose
// start sits far past the end still consumes a step for each padded nil slot. The
// fill window here is empty (zero length), so without charging steps for the gap a
// growth this large would materialize a million-element array under a small step
// quota without ever hitting the step limit or polling cancellation. The padded
// slots must be bounded by the step quota just like filled slots.
func TestArrayFillPaddedGapStepQuota(t *testing.T) {
	t.Parallel()
	source := `def run()
  [].fill(0, 1000000, 0)
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: 64 << 20}, source)
	requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
}

// TestArrayFillBoundsInitialCapacity confirms a large requested fill window does
// not reserve its full backing array up front. With a generous memory quota but
// a tiny step quota, the projected memory check passes (so execution reaches the
// loop) yet the fill must stop on the step limit after producing only a handful
// of elements. Reserving the full window beforehand would allocate
// finalLength*sizeof(Value) bytes regardless of how few steps the quota permits;
// the bounded growth path keeps the allocation proportional to the elements
// actually produced.
func TestArrayFillBoundsInitialCapacity(t *testing.T) {
	// Not parallel: this test reads process-wide allocation counters and must
	// not race other goroutines' allocations.
	const length = 100_000_000
	exec := &Execution{
		ctx:         context.Background(),
		quota:       50,
		memoryQuota: 8 << 30,
	}

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	_, err := arrayFill(exec, NewArray(nil), []Value{NewInt(0), NewInt(0), NewInt(length)}, nil, NewNil())
	goruntime.ReadMemStats(&after)

	requireErrorIs(t, err, errStepQuotaExceeded)
	if exec.steps > exec.quota+1 {
		t.Fatalf("steps = %d, want the loop to stop near the step quota %d", exec.steps, exec.quota)
	}

	// The full preallocation would have reserved length*sizeof(Value) bytes. The
	// bounded path produces only ~quota elements before failing, so its
	// allocation is many orders of magnitude smaller. Use a generous ceiling to
	// stay robust against unrelated allocation noise while still failing loudly
	// if the full capacity is ever reserved again.
	const fullPreallocBytes = uint64(length) * uint64(estimatedValueBytes)
	const ceiling = fullPreallocBytes / 1000
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > ceiling {
		t.Fatalf("fill allocated %d bytes, want <= %d (full preallocation would be %d)",
			allocated, ceiling, fullPreallocBytes)
	}
}

// TestArrayFillGrowsPastInitialCapacity confirms a fill larger than the bounded
// initial capacity grows the backing array correctly, proving the append-driven
// path produces the full, correct result rather than truncating at the initial
// capacity.
func TestArrayFillGrowsPastInitialCapacity(t *testing.T) {
	t.Parallel()
	exec := &Execution{
		ctx:         context.Background(),
		quota:       1 << 30,
		memoryQuota: 1 << 30,
	}
	const length = arrayFillInitialCap + 1000
	result, err := arrayFill(exec, NewArray(nil), []Value{NewInt(7), NewInt(0), NewInt(length)}, nil, NewNil())
	if err != nil {
		t.Fatalf("arrayFill: %v", err)
	}
	arr := result.Array()
	if len(arr) != length {
		t.Fatalf("len = %d, want %d", len(arr), length)
	}
	for i, val := range arr {
		if val.Kind() != KindInt || val.Int() != 7 {
			t.Fatalf("arr[%d] = %v, want int 7", i, val)
		}
	}
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
