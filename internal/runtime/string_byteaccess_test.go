package runtime

import "testing"

func TestStringGetbyte(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   Value
	}{
		{
			name:   "ascii index",
			script: `def run() "abc".getbyte(0) end`,
			want:   NewInt(97),
		},
		{
			// "Aé" is A (0x41) then é (0xC3 0xA9); index 1 is the first byte of é.
			name:   "multibyte first byte",
			script: `def run() "Aé".getbyte(1) end`,
			want:   NewInt(195),
		},
		{
			name:   "multibyte second byte",
			script: `def run() "Aé".getbyte(2) end`,
			want:   NewInt(169),
		},
		{
			name:   "negative index counts from end",
			script: `def run() "abc".getbyte(-1) end`,
			want:   NewInt(99),
		},
		{
			name:   "negative index reaches first byte",
			script: `def run() "abc".getbyte(-3) end`,
			want:   NewInt(97),
		},
		{
			name:   "index past end is nil",
			script: `def run() "abc".getbyte(3) end`,
			want:   NewNil(),
		},
		{
			name:   "negative index past start is nil",
			script: `def run() "abc".getbyte(-4) end`,
			want:   NewNil(),
		},
		{
			name:   "empty string is nil",
			script: `def run() "".getbyte(0) end`,
			want:   NewNil(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			requireScalarEqual(t, result, tc.want)
		})
	}
}

// TestStringGetbytePreservesInvalidUTF8 proves getbyte returns the raw byte
// value of a host-provided string without normalizing invalid UTF-8.
func TestStringGetbytePreservesInvalidUTF8(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def at(text, i)
      text.getbyte(i)
    end
    `)
	result := callFunc(t, script, "at", []Value{NewString("a\xffb"), NewInt(1)})
	requireScalarEqual(t, result, NewInt(255))
}

func TestStringGetbyteRejectMisuse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "missing argument",
			script: `def run() "abc".getbyte end`,
			want:   "string.getbyte expects exactly one index",
		},
		{
			name:   "too many arguments",
			script: `def run() "abc".getbyte(0, 1) end`,
			want:   "string.getbyte expects exactly one index",
		},
		{
			name:   "non-integer index",
			script: `def run() "abc".getbyte("x") end`,
			want:   "string.getbyte index must be an integer",
		},
		{
			name:   "keyword argument",
			script: `def run() "abc".getbyte(foo: 1) end`,
			want:   "string.getbyte does not accept keyword arguments",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestStringByteslice(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   Value
	}{
		{
			name:   "single index returns one byte string",
			script: `def run() "abc".byteslice(1) end`,
			want:   NewString("b"),
		},
		{
			name:   "single negative index",
			script: `def run() "abc".byteslice(-1) end`,
			want:   NewString("c"),
		},
		{
			name:   "single index out of range is nil",
			script: `def run() "abc".byteslice(3) end`,
			want:   NewNil(),
		},
		{
			// "Aé" bytes are 0x41 0xC3 0xA9; bytes 1..2 reassemble é.
			name:   "start and length spanning a multibyte char",
			script: `def run() "Aé".byteslice(1, 2) end`,
			want:   NewString("é"),
		},
		{
			name:   "start and length clamps to available bytes",
			script: `def run() "abc".byteslice(1, 10) end`,
			want:   NewString("bc"),
		},
		{
			name:   "start at length with length yields empty",
			script: `def run() "abc".byteslice(3, 2) end`,
			want:   NewString(""),
		},
		{
			name:   "zero length yields empty",
			script: `def run() "abc".byteslice(1, 0) end`,
			want:   NewString(""),
		},
		{
			name:   "negative start with length",
			script: `def run() "abc".byteslice(-2, 1) end`,
			want:   NewString("b"),
		},
		{
			name:   "negative length is nil",
			script: `def run() "abc".byteslice(0, -1) end`,
			want:   NewNil(),
		},
		{
			name:   "start past end is nil",
			script: `def run() "abc".byteslice(4, 1) end`,
			want:   NewNil(),
		},
		{
			name:   "negative start past beginning is nil",
			script: `def run() "abc".byteslice(-4, 1) end`,
			want:   NewNil(),
		},
		{
			name:   "inclusive range",
			script: `def run() "abcde".byteslice(1..3) end`,
			want:   NewString("bcd"),
		},
		{
			name:   "exclusive range",
			script: `def run() "abcde".byteslice(1...3) end`,
			want:   NewString("bc"),
		},
		{
			name:   "range with negative bounds",
			script: `def run() "abcde".byteslice(-3..-1) end`,
			want:   NewString("cde"),
		},
		{
			name:   "range begin past length is nil",
			script: `def run() "abc".byteslice(4..5) end`,
			want:   NewNil(),
		},
		{
			name:   "range begin at length is empty",
			script: `def run() "abc".byteslice(3..5) end`,
			want:   NewString(""),
		},
		{
			name:   "range across a multibyte char",
			script: `def run() "Aé".byteslice(1..2) end`,
			want:   NewString("é"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			requireScalarEqual(t, result, tc.want)
		})
	}
}

// TestStringBytesliceSplitsMultibyte proves slicing across a UTF-8 boundary
// returns the raw bytes verbatim rather than normalizing them, matching Ruby's
// byte-oriented semantics. A single byte of é is invalid UTF-8 on its own, so
// the result is compared by its raw bytes.
func TestStringBytesliceSplitsMultibyte(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `def run() "Aé".byteslice(1, 1) end`)
	result := callFunc(t, script, "run", nil)
	if result.Kind() != KindString {
		t.Fatalf("expected string, got %v", result.Kind())
	}
	if got := result.String(); got != "\xc3" {
		t.Fatalf("byteslice = %q, want %q", got, "\xc3")
	}
}

func TestStringBytesliceRejectMisuse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "no arguments",
			script: `def run() "abc".byteslice end`,
			want:   "string.byteslice expects an index, a range, or a start and length",
		},
		{
			name:   "too many arguments",
			script: `def run() "abc".byteslice(0, 1, 2) end`,
			want:   "string.byteslice expects an index, a range, or a start and length",
		},
		{
			name:   "non-integer single index",
			script: `def run() "abc".byteslice("x") end`,
			want:   "string.byteslice index must be an integer or range",
		},
		{
			name:   "non-integer start",
			script: `def run() "abc".byteslice("x", 1) end`,
			want:   "string.byteslice start must be an integer",
		},
		{
			name:   "non-integer length",
			script: `def run() "abc".byteslice(0, "x") end`,
			want:   "string.byteslice length must be an integer",
		},
		{
			name:   "keyword argument",
			script: `def run() "abc".byteslice(foo: 1) end`,
			want:   "string.byteslice does not accept keyword arguments",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// requireScalarEqual asserts that a scalar runtime value (nil, int, or string)
// matches the expected value, reporting kind and payload mismatches.
func requireScalarEqual(t *testing.T, got, want Value) {
	t.Helper()
	if got.Kind() != want.Kind() {
		t.Fatalf("kind = %v, want %v", got.Kind(), want.Kind())
	}
	switch want.Kind() {
	case KindNil:
		// Nothing more to compare.
	case KindInt:
		if got.Int() != want.Int() {
			t.Fatalf("int = %d, want %d", got.Int(), want.Int())
		}
	case KindString:
		if got.String() != want.String() {
			t.Fatalf("string = %q, want %q", got.String(), want.String())
		}
	default:
		t.Fatalf("unsupported scalar kind %v", want.Kind())
	}
}
