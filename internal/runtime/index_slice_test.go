package runtime

import (
	"context"
	"testing"
)

// TestArrayBracketSingleIndex covers arr[i], including Ruby's negative indexing
// and the nil-on-out-of-range behavior of Array#[] (which never raises for an
// integer index).
func TestArrayBracketSingleIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   Value
	}{
		{name: "first element", source: "def run() [10, 20, 30][0] end", want: NewInt(10)},
		{name: "interior element", source: "def run() [10, 20, 30][1] end", want: NewInt(20)},
		{name: "last element", source: "def run() [10, 20, 30][2] end", want: NewInt(30)},
		{name: "negative counts from end", source: "def run() [10, 20, 30][-1] end", want: NewInt(30)},
		{name: "most negative in range", source: "def run() [10, 20, 30][-3] end", want: NewInt(10)},
		{name: "index at length is nil", source: "def run() [10, 20, 30][3] end", want: NewNil()},
		{name: "index past end is nil", source: "def run() [1][5] end", want: NewNil()},
		{name: "negative past start is nil", source: "def run() [10, 20, 30][-4] end", want: NewNil()},
		{name: "empty array is nil", source: "def run() [][0] end", want: NewNil()},
		{name: "fractional float truncates toward zero", source: "def run() [10, 20, 30][1.9] end", want: NewInt(20)},
		{name: "negative fractional truncates toward zero", source: "def run() [10, 20, 30][-1.9] end", want: NewInt(30)},
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

// TestArrayBracketStartLength covers arr[start, length], which returns a fresh
// subarray matching Array#[](start, length): negative starts count from the
// end, a start exactly at the length yields an empty array, oversized lengths
// clamp to the suffix, and negative lengths or out-of-range starts yield nil.
func TestArrayBracketStartLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []Value
		isNil  bool
	}{
		{name: "interior window", source: "def run() [10, 20, 30, 40][1, 2] end", want: []Value{NewInt(20), NewInt(30)}},
		{name: "from start", source: "def run() [10, 20, 30][0, 2] end", want: []Value{NewInt(10), NewInt(20)}},
		{name: "negative start counts from end", source: "def run() [10, 20, 30][-2, 1] end", want: []Value{NewInt(20)}},
		{name: "oversized length clamps to suffix", source: "def run() [10, 20, 30][1, 10] end", want: []Value{NewInt(20), NewInt(30)}},
		{name: "zero length yields empty", source: "def run() [10, 20, 30][1, 0] end", want: []Value{}},
		{name: "start at length yields empty", source: "def run() [10, 20, 30][3, 1] end", want: []Value{}},
		{name: "start past length is nil", source: "def run() [10, 20, 30][4, 1] end", isNil: true},
		{name: "negative length is nil", source: "def run() [10, 20, 30][1, -1] end", isNil: true},
		{name: "negative start past beginning is nil", source: "def run() [10, 20, 30][-4, 1] end", isNil: true},
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

// TestArrayBracketRange covers arr[range]: inclusive and exclusive ranges,
// negative bounds, an end before begin yielding an empty array, and a begin past
// the length yielding nil, matching Array#[](range).
func TestArrayBracketRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []Value
		isNil  bool
	}{
		{name: "inclusive interior", source: "def run() [10, 20, 30, 40][1..2] end", want: []Value{NewInt(20), NewInt(30)}},
		{name: "exclusive interior", source: "def run() [10, 20, 30, 40][1...3] end", want: []Value{NewInt(20), NewInt(30)}},
		{name: "negative end", source: "def run() [10, 20, 30, 40][1..-1] end", want: []Value{NewInt(20), NewInt(30), NewInt(40)}},
		{name: "negative begin and end", source: "def run() [10, 20, 30, 40][-3..-2] end", want: []Value{NewInt(20), NewInt(30)}},
		{name: "end before begin is empty", source: "def run() [10, 20, 30][2..1] end", want: []Value{}},
		{name: "begin at length is empty", source: "def run() [10, 20, 30][3..5] end", want: []Value{}},
		{name: "begin past length is nil", source: "def run() [10, 20, 30][4..5] end", isNil: true},
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

func TestArrayBracketSlicesReserveQuotaBeforeCopy(t *testing.T) {
	t.Parallel()

	const receiverSize = 4096
	receiver := largeIntArray(receiverSize)

	tests := []struct {
		name      string
		indices   []Value
		slotCount int
	}{
		{
			name:      "start length",
			indices:   []Value{NewInt(0), NewInt(receiverSize)},
			slotCount: receiverSize,
		},
		{
			name:      "range",
			indices:   []Value{NewRange(Range{Start: 0, End: int64(receiverSize - 1)})},
			slotCount: receiverSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			acc := newArrayBuildAccumulator(&Execution{memoryQuota: 1 << 30}, receiver, tt.indices, nil, NewNil())
			baselineOnly := acc.projected(0)
			fullBacking := acc.projected(tt.slotCount)
			memoryQuota := (baselineOnly + fullBacking) / 2
			if memoryQuota <= baselineOnly || memoryQuota >= fullBacking {
				t.Fatalf("test quota %d does not sit strictly between baseline (%d) and full backing (%d)", memoryQuota, baselineOnly, fullBacking)
			}

			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: memoryQuota}
			_, err := exec.indexArray(&IndexExpr{}, receiver, tt.indices)
			requireErrorIs(t, err, errMemoryQuotaExceeded)
		})
	}
}

func TestArraySliceReservesQuotaBeforeCopy(t *testing.T) {
	t.Parallel()

	const receiverSize = 4096
	receiver := largeIntArray(receiverSize)

	tests := []struct {
		name      string
		args      []Value
		slotCount int
	}{
		{
			name:      "start length",
			args:      []Value{NewInt(0), NewInt(receiverSize)},
			slotCount: receiverSize,
		},
		{
			name:      "range",
			args:      []Value{NewRange(Range{Start: 0, End: int64(receiverSize - 1)})},
			slotCount: receiverSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			acc := newArrayBuildAccumulator(&Execution{memoryQuota: 1 << 30}, receiver, tt.args, nil, NewNil())
			baselineOnly := acc.projected(0)
			fullBacking := acc.projected(tt.slotCount)
			memoryQuota := (baselineOnly + fullBacking) / 2
			if memoryQuota <= baselineOnly || memoryQuota >= fullBacking {
				t.Fatalf("test quota %d does not sit strictly between baseline (%d) and full backing (%d)", memoryQuota, baselineOnly, fullBacking)
			}

			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: memoryQuota}
			_, err := callArrayMember(t, exec, receiver, "slice", tt.args, NewNil())
			requireErrorIs(t, err, errMemoryQuotaExceeded)
		})
	}
}

// TestStringBracketSingleIndex covers str[i] as a rune (character) operation,
// including negative indexing and the nil-on-out-of-range behavior of
// String#[].
func TestStringBracketSingleIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   Value
	}{
		{name: "first character", source: `def run() "abcd"[0] end`, want: NewString("a")},
		{name: "last character", source: `def run() "abcd"[3] end`, want: NewString("d")},
		{name: "negative counts from end", source: `def run() "abcd"[-1] end`, want: NewString("d")},
		{name: "most negative in range", source: `def run() "abcd"[-4] end`, want: NewString("a")},
		{name: "index at length is nil", source: `def run() "abcd"[4] end`, want: NewNil()},
		{name: "index past end is nil", source: `def run() "abcd"[10] end`, want: NewNil()},
		{name: "negative past start is nil", source: `def run() "abcd"[-5] end`, want: NewNil()},
		{name: "empty string is nil", source: `def run() ""[0] end`, want: NewNil()},
		{name: "multibyte rune indexing", source: `def run() "héllo"[1] end`, want: NewString("é")},
		{name: "multibyte negative indexing", source: `def run() "héllo"[-1] end`, want: NewString("o")},
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

// TestStringBracketStartLength covers str[start, length], which returns a rune
// substring matching String#[](start, length): a start exactly at the length
// yields an empty string, oversized lengths clamp to the suffix, and negative
// lengths or out-of-range starts yield nil.
func TestStringBracketStartLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   Value
	}{
		{name: "interior window", source: `def run() "abcd"[1, 2] end`, want: NewString("bc")},
		{name: "from start", source: `def run() "abcd"[0, 2] end`, want: NewString("ab")},
		{name: "negative start counts from end", source: `def run() "abcd"[-2, 1] end`, want: NewString("c")},
		{name: "oversized length clamps to suffix", source: `def run() "abcd"[1, 10] end`, want: NewString("bcd")},
		{name: "zero length yields empty", source: `def run() "abcd"[1, 0] end`, want: NewString("")},
		{name: "start at length yields empty", source: `def run() "abcd"[4, 1] end`, want: NewString("")},
		{name: "start past length is nil", source: `def run() "abcd"[5, 1] end`, want: NewNil()},
		{name: "negative length is nil", source: `def run() "abcd"[1, -1] end`, want: NewNil()},
		{name: "negative start past beginning is nil", source: `def run() "abcd"[-5, 1] end`, want: NewNil()},
		{name: "multibyte window", source: `def run() "héllo"[1, 2] end`, want: NewString("él")},
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

// TestStringBracketRange covers str[range] as a rune substring: inclusive and
// exclusive ranges, negative bounds, an end before begin yielding an empty
// string, and a begin past the length yielding nil, matching String#[](range).
func TestStringBracketRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   Value
	}{
		{name: "inclusive interior", source: `def run() "abcd"[1..2] end`, want: NewString("bc")},
		{name: "exclusive interior", source: `def run() "abcd"[1...3] end`, want: NewString("bc")},
		{name: "negative end", source: `def run() "abcd"[1..-1] end`, want: NewString("bcd")},
		{name: "negative begin and end", source: `def run() "abcd"[-3..-2] end`, want: NewString("bc")},
		{name: "end before begin is empty", source: `def run() "abcd"[2..1] end`, want: NewString("")},
		{name: "begin at length is empty", source: `def run() "abcd"[4..5] end`, want: NewString("")},
		{name: "begin past length is nil", source: `def run() "abcd"[5..6] end`, want: NewNil()},
		{name: "multibyte range", source: `def run() "héllo"[1..2] end`, want: NewString("él")},
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

// TestBracketIndexNegativeAssignment covers writing through a bracket target.
// Arrays accept a negative index (counting from the end) and store in place; an
// index outside the array raises rather than auto-extending.
func TestBracketIndexNegativeAssignment(t *testing.T) {
	t.Parallel()

	t.Run("negative index assigns from end", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `
        def run()
          a = [1, 2, 3]
          a[-1] = 99
          a
        end
        `)
		compareArrays(t, callFunc(t, script, "run", nil), []Value{NewInt(1), NewInt(2), NewInt(99)})
	})

	t.Run("compound assignment through negative index", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `
        def run()
          a = [1, 2, 3]
          a[-1] += 10
          a
        end
        `)
		compareArrays(t, callFunc(t, script, "run", nil), []Value{NewInt(1), NewInt(2), NewInt(13)})
	})

	t.Run("out of range negative index raises", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `
        def run()
          a = [1, 2, 3]
          a[-4] = 99
          a
        end
        `)
		requireCallErrorContains(t, script, "run", nil, CallOptions{}, "array index out of bounds")
	})
}

// TestBracketIndexRejectsMisuse covers the error cases for bracket access: a
// non-integer selector on arrays and strings, a multi-selector key on hashes,
// and indexing a type that has no bracket semantics.
func TestBracketIndexRejectsMisuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{name: "array string index", source: `def run() [1, 2, 3]["x"] end`, wantErr: "index must be integer"},
		{name: "array nil index", source: "def run() [1, 2, 3][nil] end", wantErr: "index must be integer"},
		{name: "string string index", source: `def run() "abc"["x"] end`, wantErr: "index must be integer"},
		{name: "array three selectors", source: "def run() [1, 2, 3][0, 1, 2] end", wantErr: "array index expects one index"},
		{name: "string three selectors", source: `def run() "abcd"[0, 1, 2] end`, wantErr: "string index expects one index"},
		{name: "hash multiple keys", source: "def run() {a: 1}[:a, :b] end", wantErr: "hash index expects a single key"},
		{name: "index integer", source: "def run() 1[0] end", wantErr: "cannot index"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.wantErr)
		})
	}
}
