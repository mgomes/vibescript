package runtime

import (
	"context"
	goruntime "runtime"
	"strings"
	"testing"
)

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

// TestStringBytesEnforcesMemoryQuota proves bytes rejects the call when the
// receiver string fits the memory quota but the resulting array of one Value
// per byte does not. Each byte expands to a full Value (far larger than a
// byte), so a 4KB string materializes well over 100KB; a 64KB quota admits the
// receiver yet must reject the result. The string is passed as an argument so
// the literal does not dominate the script's base memory and the quota can sit
// between the receiver and its expansion.
func TestStringBytesEnforcesMemoryQuota(t *testing.T) {
	t.Parallel()

	// 4096 ASCII bytes cost ~4KB in the estimate but expand to 4096 Values
	// (~128KB) once materialized, so a 64KB quota admits the receiver yet must
	// reject the result.
	text := strings.Repeat("vibescript", 410)[:4096]
	script := compileScriptWithConfig(t, Config{StepQuota: 20000, MemoryQuotaBytes: 64 * 1024}, `
def run(text)
  text.bytes
end
`)
	requireRunMemoryQuotaError(t, script, []Value{NewString(text)}, CallOptions{})
}

// TestStringBytesFitsAmpleMemoryQuota proves the same receiver that trips a
// tight quota in TestStringBytesEnforcesMemoryQuota succeeds under an ample
// one. This confirms it is the materialized byte array, not the receiver
// string, that the tight quota rejects, so the projected check guards the right
// allocation rather than rejecting strings that genuinely fit.
func TestStringBytesFitsAmpleMemoryQuota(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("vibescript", 410)[:4096]
	script := compileScriptWithConfig(t, Config{StepQuota: 20000, MemoryQuotaBytes: 64 << 20}, `
def run(text)
  text.bytes
end
`)
	result := callFunc(t, script, "run", []Value{NewString(text)})
	if result.Kind() != KindArray {
		t.Fatalf("expected array result, got %v", result.Kind())
	}
	if got := len(result.Array()); got != len(text) {
		t.Fatalf("len = %d, want %d", got, len(text))
	}
}

// TestStringBytesRejectsOverQuotaWithoutAllocating proves bytes rejects an
// over-quota result before reserving its backing array, not just after. The
// receiver string fits the quota but its expansion to one Value per byte does
// not; without the projected check the make([]Value, len(text)) call would
// reserve the full array (here ~10MB) and only then would the post-call memory
// check reject it, letting the call transiently exceed the sandbox limit by
// orders of magnitude. Measuring the process-wide allocation counter proves the
// rejected call never reserves that array.
func TestStringBytesRejectsOverQuotaWithoutAllocating(t *testing.T) {
	// Not parallel: this test reads process-wide allocation counters and must
	// not race other goroutines' allocations.

	const byteCount = 320 * 1024
	builtin, err := stringMember(NewString(""), "bytes")
	if err != nil {
		t.Fatalf("stringMember(bytes): %v", err)
	}
	fn := BuiltinOf(builtin).Fn

	// The receiver string costs ~byteCount bytes in the estimate, so a quota an
	// order of magnitude above it admits the receiver while the result array of
	// byteCount Values (~10MB) far exceeds it.
	exec := &Execution{ctx: context.Background(), memoryQuota: 4 << 20}
	receiver := NewString(strings.Repeat("x", byteCount))

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	_, callErr := fn(exec, receiver, nil, nil, NewNil())
	goruntime.ReadMemStats(&after)

	requireErrorIs(t, callErr, errMemoryQuotaExceeded)

	// The full backing array would reserve byteCount*sizeof(Value) bytes. The
	// projected check rejects the call before make runs, so the allocation is
	// many orders of magnitude smaller. Use a generous ceiling to stay robust
	// against unrelated allocation noise while still failing loudly if the full
	// array is ever reserved again.
	const fullArrayBytes = uint64(byteCount) * uint64(estimatedValueBytes)
	const ceiling = fullArrayBytes / 100
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > ceiling {
		t.Fatalf("bytes allocated %d bytes, want <= %d (full array would be %d)",
			allocated, ceiling, fullArrayBytes)
	}
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
