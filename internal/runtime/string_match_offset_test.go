package runtime

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStringMatchOffset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   []Value // nil means the call should return Ruby's nil
	}{
		{
			name:   "no offset keeps default behavior",
			script: `def run() "hello".match("l") end`,
			want:   []Value{NewString("l")},
		},
		{
			name:   "offset zero matches from the start",
			script: `def run() "hello".match("l", 0) end`,
			want:   []Value{NewString("l")},
		},
		{
			name:   "offset finds match at the offset",
			script: `def run() "hello".match("l", 3) end`,
			want:   []Value{NewString("l")},
		},
		{
			name:   "offset past the only match misses",
			script: `def run() "hello".match("l", 4) end`,
			want:   nil,
		},
		{
			name:   "offset equal to length on non-empty pattern misses",
			script: `def run() "hello".match("o", 5) end`,
			want:   nil,
		},
		{
			name:   "offset beyond length on char pattern misses",
			script: `def run() "hello".match("o", 99) end`,
			want:   nil,
		},
		{
			name:   "offset beyond length on single-char pattern misses",
			script: `def run() "hello".match("l", 4) end`,
			want:   nil,
		},
		{
			name:   "offset beyond length on missing char pattern misses",
			script: `def run() "abc".match("z", 4) end`,
			want:   nil,
		},
		{
			name:   "offset one past length on zero-width pattern matches empty",
			script: `def run() "hello".match("o*", 6) end`,
			want:   []Value{NewString("")},
		},
		{
			name:   "offset far past length on zero-width pattern matches empty",
			script: `def run() "hello".match("o*", 99) end`,
			want:   []Value{NewString("")},
		},
		{
			name:   "offset at length on zero-width pattern matches empty",
			script: `def run() "hello".match("o*", 5) end`,
			want:   []Value{NewString("")},
		},
		{
			name:   "offset past length on dot-star matches empty",
			script: `def run() "abc".match(".*", 4) end`,
			want:   []Value{NewString("")},
		},
		{
			name:   "offset past length on dollar anchor matches empty",
			script: `def run() "abc".match("$", 6) end`,
			want:   []Value{NewString("")},
		},
		{
			name:   "offset past length on end anchor matches empty",
			script: `def run() "abc".match("\\z", 6) end`,
			want:   []Value{NewString("")},
		},
		{
			name:   "captures retained with offset",
			script: `def run() "key=value extra=foo".match("(\\w+)=(\\w+)", 5) end`,
			want:   []Value{NewString("extra=foo"), NewString("extra"), NewString("foo")},
		},
		{
			name:   "optional capture stays nil with offset",
			script: `def run() "xxabc".match("(z)?(a)", 1) end`,
			want:   []Value{NewString("a"), NewNil(), NewString("a")},
		},
		{
			name:   "absolute anchor does not re-root at offset",
			script: `def run() "hello".match("\\Al", 2) end`,
			want:   nil,
		},
		{
			name:   "absolute anchor still holds at offset zero",
			script: `def run() "hello".match("\\Ah", 0) end`,
			want:   []Value{NewString("h")},
		},
		{
			name:   "word boundary keeps prefix context across offset",
			script: `def run() "foobar".match("\\bbar", 3) end`,
			want:   nil,
		},
		{
			name:   "word boundary matches after real boundary across offset",
			script: `def run() "foo bar".match("\\bbar", 4) end`,
			want:   []Value{NewString("bar")},
		},
		{
			name:   "empty pattern matches at end offset",
			script: `def run() "abc".match("", 3) end`,
			want:   []Value{NewString("")},
		},
		{
			name:   "empty pattern past end offset clamps and matches empty",
			script: `def run() "abc".match("", 6) end`,
			want:   []Value{NewString("")},
		},
		{
			name:   "multibyte offset is counted in runes",
			script: `def run() "héllo".match("l", 3) end`,
			want:   []Value{NewString("l")},
		},
		{
			name:   "multibyte offset selects the accented rune",
			script: `def run() "héllo".match("é", 1) end`,
			want:   []Value{NewString("é")},
		},
		{
			name:   "multibyte offset past the accented rune misses",
			script: `def run() "héllo".match("é", 2) end`,
			want:   nil,
		},
		{
			name:   "float offset truncates toward zero",
			script: `def run() "hello".match("l", 3.9) end`,
			want:   []Value{NewString("l")},
		},
		{
			name:   "negative offset counts back from the end",
			script: `def run() "hello".match("l", -2) end`,
			want:   []Value{NewString("l")},
		},
		{
			name:   "negative offset selects the final rune",
			script: `def run() "hello".match("o", -1) end`,
			want:   []Value{NewString("o")},
		},
		{
			name:   "negative offset spanning the whole string allows the absolute anchor",
			script: `def run() "hello".match("\\Ah", -5) end`,
			want:   []Value{NewString("h")},
		},
		{
			name:   "negative offset before the start misses",
			script: `def run() "hello".match("h", -99) end`,
			want:   nil,
		},
		{
			name:   "negative offset retains captures",
			script: `def run() "a=1 b=2".match("(\\w)=(\\d)", -3) end`,
			want:   []Value{NewString("b=2"), NewString("b"), NewString("2")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if tc.want == nil {
				if result.Kind() != KindNil {
					t.Fatalf("match = %v, want nil", result)
				}
				return
			}
			compareArrays(t, result, tc.want)
		})
	}
}

func TestStringMatchOffsetErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "missing pattern",
			script: `def run() "abc".match() end`,
			want:   "string.match expects a pattern and optional offset",
		},
		{
			name:   "too many arguments",
			script: `def run() "abc".match("a", 1, 2) end`,
			want:   "string.match expects a pattern and optional offset",
		},
		{
			name:   "non-string pattern",
			script: `def run() "abc".match(123) end`,
			want:   "string.match pattern must be string",
		},
		{
			name:   "non-integer offset",
			script: `def run() "abc".match("a", "1") end`,
			want:   "string.match offset must be integer",
		},
		{
			name:   "invalid regex pattern",
			script: `def run() "abc".match("(") end`,
			want:   "string.match invalid regex",
		},
		{
			name:   "invalid regex pattern with offset",
			script: `def run() "abcdef".match("(", 2) end`,
			want:   "string.match invalid regex",
		},
		{
			name:   "invalid regex pattern with out-of-range offset",
			script: `def run() "abc".match("(", 99) end`,
			want:   "string.match invalid regex",
		},
		{
			name:   "invalid regex pattern with out-of-range negative offset",
			script: `def run() "abc".match("(", -99) end`,
			want:   "string.match invalid regex",
		},
		{
			name:   "keyword argument",
			script: `def run() "abc".match("a", foo: true) end`,
			want:   "string.match does not accept keyword arguments",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestStringMatchOffsetSizeGuards(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 8 << 20}, `def match_text(text, pattern)
  text.match(pattern)
end

def match_text_offset(text, pattern, offset)
  text.match(pattern, offset)
end`)

	largePattern := strings.Repeat("a", maxRegexPatternSize+1)
	requireCallErrorContains(t, script, "match_text", []Value{NewString("aaa"), NewString(largePattern)}, CallOptions{}, "string.match pattern exceeds limit")

	largeText := strings.Repeat("a", maxRegexInputBytes+1)
	requireCallErrorContains(t, script, "match_text", []Value{NewString(largeText), NewString("a")}, CallOptions{}, "string.match text exceeds limit")

	// Guards apply on the offset path before the prefix wrapper is assembled.
	requireCallErrorContains(t, script, "match_text_offset", []Value{NewString(largeText), NewString("a"), NewInt(1)}, CallOptions{}, "string.match text exceeds limit")
}

func TestRegexSubmatchFromRuneOffsetLargeOffsetCrossesRepeatLimit(t *testing.T) {
	t.Parallel()

	// Go's regexp rejects counted repetitions above 1000, so the offset wrapper
	// must not skip the prefix with a {N} repetition. The fixed-size wrapper
	// handles arbitrary offsets; exercise one well beyond that bound to lock the
	// behavior in.
	text := strings.Repeat("x", 2000) + "needle"

	indices, err := regexSubmatchFromRuneOffset("string.match", text, "needle", 1500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if indices == nil {
		t.Fatalf("match at offset 1500 = nil, want a match")
	}
	if got := text[indices[0]:indices[1]]; got != "needle" {
		t.Fatalf("match at offset 1500 = %q, want %q", got, "needle")
	}

	indices, err = regexSubmatchFromRuneOffset("string.match", text, "needle", 2001)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if indices != nil {
		t.Fatalf("match at offset 2001 = %v, want nil", indices)
	}
}

func TestRegexSubmatchFromRuneOffsetDoesNotEmbedSubjectPrefix(t *testing.T) {
	t.Parallel()

	// A near-end offset on a large subject must not compile a pattern that grows
	// with the prefix: doing so would blow past the pattern-size guard, spend
	// seconds compiling, and retain megabytes per distinct text/offset in the
	// regex cache. Use a fresh cache so the assertion is parallel-safe.
	cache := newRegexCache(compiledRegexCacheCapacity)
	prefix := strings.Repeat("x", maxRegexPatternSize*4)
	text := prefix + "needle"
	offset := utf8.RuneCountInString(prefix)

	indices, err := regexSubmatchFromRuneOffsetWithCache(cache, "string.match", text, "needle", offset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if indices == nil {
		t.Fatalf("match at offset %d = nil, want a match", offset)
	}
	if got := text[indices[0]:indices[1]]; got != "needle" {
		t.Fatalf("match at offset %d = %q, want %q", offset, got, "needle")
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	for pattern := range cache.entries {
		if len(pattern) > maxRegexPatternSize {
			t.Fatalf("cached compiled pattern of %d bytes exceeds guard %d; subject prefix leaked into the regex", len(pattern), maxRegexPatternSize)
		}
	}
}

func TestRegexSubmatchFromRuneOffsetValidatesPatternRegardlessOfOffset(t *testing.T) {
	t.Parallel()

	// An out-of-range offset must still report an invalid pattern: the offset only
	// decides the match result, never whether a bad regex is accepted. The bad
	// pattern is also never rewritten into the offset wrapper, so nothing oversized
	// is compiled or cached. Use fresh caches so the assertions are parallel-safe.
	for _, offset := range []int{3, 99} {
		cache := newRegexCache(compiledRegexCacheCapacity)
		indices, err := regexSubmatchFromRuneOffsetWithCache(cache, "string.match", "abc", "(", offset)
		if err == nil {
			t.Fatalf("offset %d: expected invalid regex error, got indices=%v", offset, indices)
		}
		if !strings.Contains(err.Error(), "string.match invalid regex") {
			t.Fatalf("offset %d: error = %q, want it to contain %q", offset, err.Error(), "string.match invalid regex")
		}
	}
}
