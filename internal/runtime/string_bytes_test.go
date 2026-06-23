package runtime

import "testing"

func TestStringBytes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name:   "ascii",
			script: `def run() "abc".bytes end`,
			want:   []Value{NewInt(97), NewInt(98), NewInt(99)},
		},
		{
			// "héllo" is h, then é (UTF-8 0xC3 0xA9), then l, l, o.
			name:   "multibyte runes expand to their utf-8 bytes",
			script: `def run() "héllo".bytes end`,
			want:   []Value{NewInt(104), NewInt(195), NewInt(169), NewInt(108), NewInt(108), NewInt(111)},
		},
		{
			name:   "empty string",
			script: `def run() "".bytes end`,
			want:   []Value{},
		},
		{
			name:   "newline byte",
			script: "def run() \"a\\nb\".bytes end",
			want:   []Value{NewInt(97), NewInt(10), NewInt(98)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			compareArrays(t, result, tc.want)
		})
	}
}

// TestStringBytesPreservesInvalidUTF8 proves bytes returns the raw byte values
// of a host-provided string without normalizing invalid UTF-8 sequences,
// matching Ruby's String#bytes. Invalid bytes cannot appear in Vibescript
// literals, so the string arrives through a function argument.
func TestStringBytesPreservesInvalidUTF8(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def to_bytes(text)
      text.bytes
    end
    `)

	result := callFunc(t, script, "to_bytes", []Value{NewString("a\xffb")})
	compareArrays(t, result, []Value{NewInt(97), NewInt(255), NewInt(98)})
}

func TestStringEachByte(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name:   "ascii yields each byte",
			script: "def run() out = [] \"ab\".each_byte { |b| out = out + [b] } out end",
			want:   []Value{NewInt(97), NewInt(98)},
		},
		{
			name:   "multibyte yields utf-8 bytes",
			script: "def run() out = [] \"é\".each_byte { |b| out = out + [b] } out end",
			want:   []Value{NewInt(195), NewInt(169)},
		},
		{
			name:   "empty string yields nothing",
			script: "def run() out = [] \"\".each_byte { |b| out = out + [b] } out end",
			want:   []Value{},
		},
		{
			name:   "newline byte is yielded",
			script: "def run() out = [] \"a\\nb\".each_byte { |b| out = out + [b] } out end",
			want:   []Value{NewInt(97), NewInt(10), NewInt(98)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			compareArrays(t, result, tc.want)
		})
	}
}

func TestStringEachByteReturnsReceiver(t *testing.T) {
	t.Parallel()
	script := compileScript(t, "def run() \"abc\".each_byte { |b| b } end")
	result := callFunc(t, script, "run", nil)
	if result.Kind() != KindString || result.String() != "abc" {
		t.Fatalf("expected receiver string %q, got %v %q", "abc", result.Kind(), result.String())
	}
}

// TestStringEachBytePreservesInvalidUTF8 proves each_byte yields the raw byte
// values of a host-provided string without normalizing invalid UTF-8.
func TestStringEachBytePreservesInvalidUTF8(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def collect(text)
      out = []
      text.each_byte { |b| out = out + [b] }
      out
    end
    `)

	result := callFunc(t, script, "collect", []Value{NewString("a\xffb")})
	compareArrays(t, result, []Value{NewInt(97), NewInt(255), NewInt(98)})
}

func TestStringEachByteShortCircuitsOnBlockError(t *testing.T) {
	t.Parallel()

	// each_byte streams byte by byte, so a block error must stop iteration
	// immediately. Collecting bytes seen before the raise proves only the bytes
	// up to the failure were yielded.
	script := compileScript(t, `
def run()
  seen = []
  begin
    "abc".each_byte do |b|
      seen = seen + [b]
      if b == 98
        raise "boom"
      end
    end
  rescue
    nil
  end
  seen
end
`)
	result := callFunc(t, script, "run", nil)
	compareArrays(t, result, []Value{NewInt(97), NewInt(98)})
}

func TestStringBytesEachByteRejectMisuse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "bytes rejects positional arguments",
			script: `def run() "abc".bytes("x") end`,
			want:   "string.bytes does not take arguments",
		},
		{
			name:   "bytes rejects keyword arguments",
			script: `def run() "abc".bytes(foo: 1) end`,
			want:   "string.bytes does not take arguments",
		},
		{
			name:   "each_byte requires a block",
			script: `def run() "ab".each_byte end`,
			want:   "string.each_byte requires a block",
		},
		{
			name:   "each_byte rejects positional arguments",
			script: `def run() "ab".each_byte("x") { |b| b } end`,
			want:   "string.each_byte does not take arguments",
		},
		{
			name:   "each_byte rejects keyword arguments",
			script: `def run() "ab".each_byte(foo: 1) { |b| b } end`,
			want:   "string.each_byte does not take arguments",
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
