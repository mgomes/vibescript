package runtime

import "testing"

// TestArrayAtReturnsElement covers Array#at, which returns the single element at
// an index. A negative index counts back from the end and an out-of-range index
// yields nil, matching Ruby's Array#at.
func TestArrayAtReturnsElement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   Value
	}{
		{name: "first element", source: "def run() [10, 20, 30].at(0) end", want: NewInt(10)},
		{name: "interior element", source: "def run() [10, 20, 30].at(1) end", want: NewInt(20)},
		{name: "last element", source: "def run() [10, 20, 30].at(2) end", want: NewInt(30)},
		{name: "negative counts from end", source: "def run() [10, 20, 30].at(-1) end", want: NewInt(30)},
		{name: "most negative in range", source: "def run() [10, 20, 30].at(-3) end", want: NewInt(10)},
		{name: "index past end is nil", source: "def run() [10, 20, 30].at(3) end", want: NewNil()},
		{name: "large index is nil", source: "def run() [10, 20].at(9) end", want: NewNil()},
		{name: "negative past start is nil", source: "def run() [10, 20, 30].at(-4) end", want: NewNil()},
		{name: "empty array is nil", source: "def run() [].at(0) end", want: NewNil()},
		{name: "fractional float truncates toward zero", source: "def run() [10, 20, 30].at(1.9) end", want: NewInt(20)},
		{name: "negative fractional float truncates toward zero", source: "def run() [10, 20, 30].at(-1.9) end", want: NewInt(30)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tt.want) {
				t.Fatalf("%s = %v, want %v", tt.source, got, tt.want)
			}
		})
	}
}

// TestArrayAtMatchesBracketAccess verifies that Array#at agrees with bracket
// indexing for every in-range non-negative index, the parity the issue calls
// for.
func TestArrayAtMatchesBracketAccess(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def compare()
      values = [10, 20, 30]
      mismatches = []
      i = 0
      while i < values.size
        if values.at(i) != values[i]
          mismatches = mismatches.push(i)
        end
        i = i + 1
      end
      mismatches
    end
    `)

	compareArrays(t, callFunc(t, script, "compare", nil), []Value{})
}

// TestArrayBracketMatchesAtSliceDigNilOnMiss pins the behavior the #419
// changelog relies on for its migration advice: bracket indexing, at, slice, and
// dig all return nil for an out-of-range index and count negative indices from
// the end, matching Ruby's Array#[]. Only fetch raises on a miss. Keeping these
// locked in stops the changelog's "use [], at, slice, or dig for nil-on-miss"
// guidance from drifting back to the pre-#408 raising behavior.
func TestArrayBracketMatchesAtSliceDigNilOnMiss(t *testing.T) {
	t.Parallel()

	nilOnMiss := []struct {
		name   string
		source string
	}{
		{name: "bracket past end is nil", source: "def run() [10, 20, 30][9] end"},
		{name: "bracket negative past start is nil", source: "def run() [10, 20, 30][-4] end"},
		{name: "at past end is nil", source: "def run() [10, 20, 30].at(9) end"},
		{name: "at negative past start is nil", source: "def run() [10, 20, 30].at(-4) end"},
		{name: "slice past end is nil", source: "def run() [10, 20, 30].slice(9) end"},
		{name: "dig past end is nil", source: "def run() [10, 20, 30].dig(9) end"},
	}
	for _, tt := range nilOnMiss {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			got := callFunc(t, script, "run", nil)
			if !got.Equal(NewNil()) {
				t.Fatalf("%s = %v, want nil", tt.source, got)
			}
		})
	}

	lastElement := []struct {
		name   string
		source string
	}{
		{name: "bracket negative counts from end", source: "def run() [10, 20, 30][-1] end"},
		{name: "at negative counts from end", source: "def run() [10, 20, 30].at(-1) end"},
		{name: "slice negative counts from end", source: "def run() [10, 20, 30].slice(-1) end"},
	}
	for _, tt := range lastElement {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			got := callFunc(t, script, "run", nil)
			if !got.Equal(NewInt(30)) {
				t.Fatalf("%s = %v, want 30", tt.source, got)
			}
		})
	}

	// fetch is the only accessor that raises on a miss; it anchors the
	// changelog's "use fetch when a miss should raise" guidance.
	t.Run("fetch past end raises", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, "def run() [10, 20, 30].fetch(9) end")
		requireCallErrorContains(t, script, "run", nil, CallOptions{}, "outside of array bounds")
	})
}

// TestArrayAtRejectsMisuse covers the argument validation for Array#at.
func TestArrayAtRejectsMisuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{name: "no arguments", source: "def run() [1, 2, 3].at() end", wantErr: "array.at expects exactly one index"},
		{name: "too many arguments", source: "def run() [1, 2, 3].at(0, 1) end", wantErr: "array.at expects exactly one index"},
		{name: "string index", source: `def run() [1, 2, 3].at("0") end`, wantErr: "array.at index must be integer"},
		{name: "nil index", source: "def run() [1, 2, 3].at(nil) end", wantErr: "array.at index must be integer"},
		{name: "keyword argument", source: "def run() [1, 2, 3].at(index: 0) end", wantErr: "array.at does not take keyword arguments"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.wantErr)
		})
	}
}

// TestArraySliceSingleIndex covers the single-index form of Array#slice, which
// behaves like Array#at and Array#[]: it returns the element at the index rather
// than a subarray, counting a negative index from the end and yielding nil out
// of range.
func TestArraySliceSingleIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   Value
	}{
		{name: "first element", source: "def run() [10, 20, 30].slice(0) end", want: NewInt(10)},
		{name: "last element", source: "def run() [10, 20, 30].slice(2) end", want: NewInt(30)},
		{name: "negative counts from end", source: "def run() [10, 20, 30].slice(-1) end", want: NewInt(30)},
		{name: "index at length is nil", source: "def run() [10, 20, 30].slice(3) end", want: NewNil()},
		{name: "index past end is nil", source: "def run() [10, 20].slice(9) end", want: NewNil()},
		{name: "negative past start is nil", source: "def run() [10, 20, 30].slice(-4) end", want: NewNil()},
		{name: "fractional float truncates", source: "def run() [10, 20, 30].slice(1.9) end", want: NewInt(20)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tt.want) {
				t.Fatalf("%s = %v, want %v", tt.source, got, tt.want)
			}
		})
	}
}

// TestArraySliceStartLength covers the (start, length) form of Array#slice,
// which returns a fresh subarray. It mirrors Ruby's handling of negative starts,
// a start exactly at the length, oversized lengths, zero lengths, and negative
// lengths.
func TestArraySliceStartLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []Value
		isNil  bool
	}{
		{name: "interior window", source: "def run() [10, 20, 30, 40].slice(1, 2) end", want: []Value{NewInt(20), NewInt(30)}},
		{name: "from start", source: "def run() [10, 20, 30].slice(0, 2) end", want: []Value{NewInt(10), NewInt(20)}},
		{name: "negative start counts from end", source: "def run() [10, 20, 30].slice(-2, 1) end", want: []Value{NewInt(20)}},
		{name: "oversized length clamps to suffix", source: "def run() [10, 20, 30].slice(1, 10) end", want: []Value{NewInt(20), NewInt(30)}},
		{name: "zero length yields empty", source: "def run() [10, 20, 30].slice(1, 0) end", want: []Value{}},
		{name: "start at length yields empty", source: "def run() [10, 20, 30].slice(3, 1) end", want: []Value{}},
		{name: "start past length is nil", source: "def run() [10, 20, 30].slice(4, 0) end", isNil: true},
		{name: "negative length is nil", source: "def run() [10, 20, 30].slice(1, -1) end", isNil: true},
		{name: "negative start past beginning is nil", source: "def run() [10, 20, 30].slice(-4, 1) end", isNil: true},
		{name: "fractional length truncates", source: "def run() [10, 20, 30].slice(0, 2.9) end", want: []Value{NewInt(10), NewInt(20)}},
		// Ruby: [1].slice(1, 9223372036854775807) => []. The near-MaxInt64 length
		// must clamp to the suffix without overflowing start+length into a negative
		// make size, which previously panicked and crashed the host.
		{name: "near max length clamps to suffix", source: "def run() [1].slice(1, 9223372036854775807) end", want: []Value{}},
		{name: "near max length from interior", source: "def run() [10, 20, 30].slice(1, 9223372036854775807) end", want: []Value{NewInt(20), NewInt(30)}},
		// Ruby: [1, 2, 3].slice(9223372036854775807, 2) => nil. A near-MaxInt64 start
		// lands far past the array and yields nil rather than overflowing.
		{name: "near max start is nil", source: "def run() [1, 2, 3].slice(9223372036854775807, 2) end", isNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			got := callFunc(t, script, "run", nil)
			if tt.isNil {
				if got.Kind() != KindNil {
					t.Fatalf("%s = %v, want nil", tt.source, got)
				}
				return
			}
			compareArrays(t, got, tt.want)
		})
	}
}

// TestArraySliceRange covers the range form of Array#slice, aligning with the
// range slicing already available for strings: negative bounds count from the
// end, exclusive ranges drop the end, an end before begin yields an empty array,
// and a begin past the length yields nil.
func TestArraySliceRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []Value
		isNil  bool
	}{
		{name: "inclusive range", source: "def run() [1, 2, 3, 4].slice(1..2) end", want: []Value{NewInt(2), NewInt(3)}},
		{name: "exclusive range", source: "def run() [1, 2, 3, 4].slice(1...3) end", want: []Value{NewInt(2), NewInt(3)}},
		{name: "negative bounds", source: "def run() [1, 2, 3, 4].slice(-3..-1) end", want: []Value{NewInt(2), NewInt(3), NewInt(4)}},
		{name: "end past length clamps", source: "def run() [1, 2, 3].slice(1..9) end", want: []Value{NewInt(2), NewInt(3)}},
		{name: "begin at length yields empty", source: "def run() [1, 2, 3].slice(3..5) end", want: []Value{}},
		{name: "end before begin yields empty", source: "def run() [1, 2, 3].slice(2..1) end", want: []Value{}},
		{name: "begin past length is nil", source: "def run() [1, 2, 3].slice(4..5) end", isNil: true},
		{name: "begin too negative is nil", source: "def run() [1, 2, 3].slice(-4..-1) end", isNil: true},
		// Ruby: [10, 20, 30].slice(1..9223372036854775807) => [20, 30]. The inclusive
		// end at math.MaxInt64 must clamp to the array length instead of wrapping the
		// exclusive-end increment to a negative no-op window.
		{name: "inclusive end at max clamps", source: "def run() [10, 20, 30].slice(1..9223372036854775807) end", want: []Value{NewInt(20), NewInt(30)}},
		{name: "exclusive end at max clamps", source: "def run() [10, 20, 30].slice(1...9223372036854775807) end", want: []Value{NewInt(20), NewInt(30)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			got := callFunc(t, script, "run", nil)
			if tt.isNil {
				if got.Kind() != KindNil {
					t.Fatalf("%s = %v, want nil", tt.source, got)
				}
				return
			}
			compareArrays(t, got, tt.want)
		})
	}
}

// TestArraySliceReturnsIndependentCopy verifies that the subarray forms of
// Array#slice do not alias the receiver's backing array, so the original is left
// untouched even when the result is mutated through a push.
func TestArraySliceReturnsIndependentCopy(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      original = [1, 2, 3, 4]
      part = original.slice(1, 2)
      grown = part.push(99)
      { original: original, grown: grown }
    end
    `)

	got := callFunc(t, script, "run", nil).Hash()
	compareArrays(t, got["original"], []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})
	compareArrays(t, got["grown"], []Value{NewInt(2), NewInt(3), NewInt(99)})
}

// TestArraySliceRejectsMisuse covers the argument validation for Array#slice.
func TestArraySliceRejectsMisuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{name: "no arguments", source: "def run() [1, 2, 3].slice() end", wantErr: "array.slice expects an index, a start and length, or a range"},
		{name: "too many arguments", source: "def run() [1, 2, 3].slice(0, 1, 2) end", wantErr: "array.slice expects an index, a start and length, or a range"},
		{name: "string index", source: `def run() [1, 2, 3].slice("0") end`, wantErr: "array.slice index must be integer"},
		{name: "string start", source: `def run() [1, 2, 3].slice("0", 1) end`, wantErr: "array.slice index must be integer"},
		{name: "string length", source: `def run() [1, 2, 3].slice(0, "1") end`, wantErr: "array.slice length must be integer"},
		{name: "nil length", source: "def run() [1, 2, 3].slice(0, nil) end", wantErr: "array.slice length must be integer"},
		{name: "keyword argument", source: "def run() [1, 2, 3].slice(start: 0) end", wantErr: "array.slice does not take keyword arguments"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.wantErr)
		})
	}
}
