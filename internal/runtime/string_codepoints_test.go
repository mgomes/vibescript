package runtime

import (
	"strings"
	"testing"
)

func TestStringCodepoints(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name:   "ascii",
			script: `def run() "abc".codepoints end`,
			want:   []Value{NewInt(97), NewInt(98), NewInt(99)},
		},
		{
			// "Aé" is A (U+0041) then é (U+00E9), so two code points even though
			// é encodes to two UTF-8 bytes.
			name:   "multibyte runes are single code points",
			script: `def run() "Aé".codepoints end`,
			want:   []Value{NewInt(65), NewInt(233)},
		},
		{
			name:   "emoji is one code point",
			script: `def run() "😀".codepoints end`,
			want:   []Value{NewInt(128512)},
		},
		{
			name:   "empty string",
			script: `def run() "".codepoints end`,
			want:   []Value{},
		},
		{
			name:   "newline code point",
			script: "def run() \"a\\nb\".codepoints end",
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

func TestStringEachCodepoint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name:   "ascii yields each code point",
			script: "def run() out = [] \"ab\".each_codepoint { |c| out = out + [c] } out end",
			want:   []Value{NewInt(97), NewInt(98)},
		},
		{
			name:   "multibyte yields single code points",
			script: "def run() out = [] \"Aé\".each_codepoint { |c| out = out + [c] } out end",
			want:   []Value{NewInt(65), NewInt(233)},
		},
		{
			name:   "empty string yields nothing",
			script: "def run() out = [] \"\".each_codepoint { |c| out = out + [c] } out end",
			want:   []Value{},
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

func TestStringEachCodepointReturnsReceiver(t *testing.T) {
	t.Parallel()
	script := compileScript(t, "def run() \"abc\".each_codepoint { |c| c } end")
	result := callFunc(t, script, "run", nil)
	if result.Kind() != KindString || result.String() != "abc" {
		t.Fatalf("expected receiver string %q, got %v %q", "abc", result.Kind(), result.String())
	}
}

func TestStringEachCodepointShortCircuitsOnBlockError(t *testing.T) {
	t.Parallel()

	// each_codepoint streams code point by code point, so a block error must stop
	// iteration immediately. Collecting code points seen before the raise proves
	// only those up to the failure were yielded.
	script := compileScript(t, `
def run()
  seen = []
  begin
    "abc".each_codepoint do |c|
      seen = seen + [c]
      if c == 98
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

// TestStringCodepointsEnforcesMemoryQuota proves codepoints rejects the call
// when the receiver string fits the memory quota but the resulting array of one
// Value per code point does not. Each code point expands to a full Value (far
// larger than a byte), so a 4KB ASCII string materializes well over 100KB; a
// 64KB quota admits the receiver yet must reject the result.
func TestStringCodepointsEnforcesMemoryQuota(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("vibescript", 410)[:4096]
	script := compileScriptWithConfig(t, Config{StepQuota: 20000, MemoryQuotaBytes: 64 * 1024}, `
def run(text)
  text.codepoints
end
`)
	requireRunMemoryQuotaError(t, script, []Value{NewString(text)}, CallOptions{})
}

// TestStringCodepointsFitsAmpleMemoryQuota proves the same receiver that trips a
// tight quota succeeds under an ample one, confirming it is the materialized
// code-point array, not the receiver string, that the tight quota rejects.
func TestStringCodepointsFitsAmpleMemoryQuota(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("vibescript", 410)[:4096]
	script := compileScriptWithConfig(t, Config{StepQuota: 20000, MemoryQuotaBytes: 64 << 20}, `
def run(text)
  text.codepoints
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

func TestStringCodepointsEachCodepointRejectMisuse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "codepoints rejects positional arguments",
			script: `def run() "abc".codepoints("x") end`,
			want:   "string.codepoints does not take arguments",
		},
		{
			name:   "codepoints rejects keyword arguments",
			script: `def run() "abc".codepoints(foo: 1) end`,
			want:   "string.codepoints does not take arguments",
		},
		{
			name:   "each_codepoint requires a block",
			script: `def run() "ab".each_codepoint end`,
			want:   "string.each_codepoint requires a block",
		},
		{
			name:   "each_codepoint rejects positional arguments",
			script: `def run() "ab".each_codepoint("x") { |c| c } end`,
			want:   "string.each_codepoint does not take arguments",
		},
		{
			name:   "each_codepoint rejects keyword arguments",
			script: `def run() "ab".each_codepoint(foo: 1) { |c| c } end`,
			want:   "string.each_codepoint does not take arguments",
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
