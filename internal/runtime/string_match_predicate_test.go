package runtime

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
)

func TestStringMatchPredicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   bool
	}{
		{
			name:   "literal pattern hit",
			script: `def run() "abc".match?("b") end`,
			want:   true,
		},
		{
			name:   "literal pattern miss",
			script: `def run() "abc".match?("z") end`,
			want:   false,
		},
		{
			name:   "regex character class hit",
			script: `def run() "ID-12".match?("ID-[0-9]+") end`,
			want:   true,
		},
		{
			name:   "regex character class miss",
			script: `def run() "ID-AB".match?("ID-[0-9]+") end`,
			want:   false,
		},
		{
			name:   "anchored pattern at string start",
			script: `def run() "abc".match?("\\Aabc") end`,
			want:   true,
		},
		{
			name:   "offset zero keeps default behavior",
			script: `def run() "abc".match?("b", 0) end`,
			want:   true,
		},
		{
			name:   "offset before match start hits",
			script: `def run() "abc".match?("b", 1) end`,
			want:   true,
		},
		{
			name:   "offset past match start misses",
			script: `def run() "abc".match?("b", 2) end`,
			want:   false,
		},
		{
			name:   "offset allows overlapping match",
			script: `def run() "aaa".match?("aa", 1) end`,
			want:   true,
		},
		{
			name:   "offset past overlapping match misses",
			script: `def run() "aaa".match?("aa", 2) end`,
			want:   false,
		},
		{
			name:   "word boundary keeps prefix context across offset",
			script: `def run() "foobar".match?("\\bbar", 3) end`,
			want:   false,
		},
		{
			name:   "word boundary matches after real boundary across offset",
			script: `def run() "foo bar".match?("\\bbar", 4) end`,
			want:   true,
		},
		{
			name:   "anchor does not re-root at offset",
			script: `def run() "aabc".match?("\\Aabc", 1) end`,
			want:   false,
		},
		{
			name:   "match found after the offset",
			script: `def run() "xxxabc".match?("bc", 4) end`,
			want:   true,
		},
		{
			name:   "offset past the match start misses",
			script: `def run() "xxxabc".match?("bc", 5) end`,
			want:   false,
		},
		{
			name:   "empty pattern matches at end offset",
			script: `def run() "abc".match?("", 3) end`,
			want:   true,
		},
		{
			name:   "non-empty pattern at end offset misses",
			script: `def run() "abc".match?("a", 3) end`,
			want:   false,
		},
		{
			name:   "offset past length misses without error",
			script: `def run() "abc".match?("a", 5) end`,
			want:   false,
		},
		{
			name:   "multibyte offset is counted in runes",
			script: `def run() "héllo wörld".match?("wörld", 6) end`,
			want:   true,
		},
		{
			name:   "multibyte offset past match misses",
			script: `def run() "héllo wörld".match?("wörld", 7) end`,
			want:   false,
		},
		{
			name:   "float offset truncates toward zero",
			script: `def run() "abc".match?("c", 2.9) end`,
			want:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindBool {
				t.Fatalf("expected bool, got %v", result.Kind())
			}
			if got := result.Bool(); got != tc.want {
				t.Fatalf("match? = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStringMatchPredicateErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "missing pattern",
			script: `def run() "abc".match?() end`,
			want:   "string.match? expects a pattern and optional offset",
		},
		{
			name:   "too many arguments",
			script: `def run() "abc".match?("a", 1, 2) end`,
			want:   "string.match? expects a pattern and optional offset",
		},
		{
			name:   "non-string pattern",
			script: `def run() "abc".match?(123) end`,
			want:   "string.match? pattern must be string",
		},
		{
			name:   "negative offset",
			script: `def run() "abc".match?("a", -1) end`,
			want:   "string.match? offset must be non-negative integer",
		},
		{
			name:   "non-integer offset",
			script: `def run() "abc".match?("a", "1") end`,
			want:   "string.match? offset must be non-negative integer",
		},
		{
			name:   "invalid regex pattern",
			script: `def run() "abc".match?("(") end`,
			want:   "string.match? invalid regex",
		},
		{
			name:   "invalid regex pattern with offset",
			script: `def run() "abcdef".match?("(", 2) end`,
			want:   "string.match? invalid regex",
		},
		{
			name:   "invalid regex pattern with out-of-range offset",
			script: `def run() "abc".match?("(", 99) end`,
			want:   "string.match? invalid regex",
		},
		{
			name:   "invalid regex pattern at end-of-string offset",
			script: `def run() "abc".match?("(", 3) end`,
			want:   "string.match? invalid regex",
		},
		{
			name:   "keyword argument",
			script: `def run() "abc".match?("a", foo: true) end`,
			want:   "string.match? does not accept keyword arguments",
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

func TestStringMatchPredicateSizeGuards(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 8 << 20}, `def match_text(text, pattern)
  text.match?(pattern)
end

def match_text_offset(text, pattern, offset)
  text.match?(pattern, offset)
end`)

	largePattern := strings.Repeat("a", maxRegexPatternSize+1)
	requireCallErrorContains(t, script, "match_text", []Value{NewString("aaa"), NewString(largePattern)}, CallOptions{}, "string.match? pattern exceeds limit")

	largeText := strings.Repeat("a", maxRegexInputBytes+1)
	requireCallErrorContains(t, script, "match_text", []Value{NewString(largeText), NewString("a")}, CallOptions{}, "string.match? text exceeds limit")

	// Guards apply on the offset path before the prefix wrapper is assembled.
	requireCallErrorContains(t, script, "match_text_offset", []Value{NewString(largeText), NewString("a"), NewInt(1)}, CallOptions{}, "string.match? text exceeds limit")
}

func TestStringMatchPredicateLargeOffsetCrossesRepeatLimit(t *testing.T) {
	t.Parallel()

	// Go's regexp rejects counted repetitions above 1000, so the offset wrapper
	// must not skip the prefix with a {N} repetition. The fixed-size wrapper
	// handles arbitrary offsets; exercise one well beyond that bound to lock the
	// behavior in.
	text := strings.Repeat("x", 2000) + "needle"

	got, err := regexMatchFromRuneOffset("string.match?", text, "needle", 1500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := true; got != want {
		t.Fatalf("match at offset 1500 = %v, want %v", got, want)
	}

	got, err = regexMatchFromRuneOffset("string.match?", text, "needle", 2001)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := false; got != want {
		t.Fatalf("match at offset 2001 = %v, want %v", got, want)
	}
}

func TestRegexMatchFromRuneOffsetDoesNotEmbedSubjectPrefix(t *testing.T) {
	t.Parallel()

	// A near-end offset on a large subject must not compile a pattern that grows
	// with the prefix: doing so would blow past the pattern-size guard, spend
	// seconds compiling, and retain megabytes per distinct text/offset in the
	// regex cache. Use a fresh cache so the assertion is parallel-safe.
	cache := newRegexCache(compiledRegexCacheCapacity)
	prefix := strings.Repeat("x", maxRegexPatternSize*4)
	text := prefix + "needle"
	offset := utf8.RuneCountInString(prefix)

	matched, err := regexMatchFromRuneOffsetWithCache(cache, "string.match?", text, "needle", offset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !matched {
		t.Fatalf("match at offset %d = false, want true", offset)
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	for pattern := range cache.entries {
		if len(pattern) > maxRegexPatternSize {
			t.Fatalf("cached compiled pattern of %d bytes exceeds guard %d; subject prefix leaked into the regex", len(pattern), maxRegexPatternSize)
		}
	}
}

func TestRegexMatchFromRuneOffsetValidatesPatternRegardlessOfOffset(t *testing.T) {
	t.Parallel()

	// An out-of-range offset must still report an invalid pattern: the offset only
	// decides the match result, never whether a bad regex is accepted. The bad
	// pattern is also never rewritten into the offset wrapper, so nothing oversized
	// is compiled or cached. Use fresh caches so the assertions are parallel-safe.
	for _, offset := range []int{3, 99} {
		cache := newRegexCache(compiledRegexCacheCapacity)
		matched, err := regexMatchFromRuneOffsetWithCache(cache, "string.match?", "abc", "(", offset)
		if err == nil {
			t.Fatalf("offset %d: expected invalid regex error, got match=%v", offset, matched)
		}
		if !strings.Contains(err.Error(), "string.match? invalid regex") {
			t.Fatalf("offset %d: error = %q, want it to contain %q", offset, err.Error(), "string.match? invalid regex")
		}
	}
}

func TestRegexMatchFromRuneOffset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		pattern string
		offset  int
		want    bool
	}{
		{name: "offset zero hit", text: "abc", pattern: "b", offset: 0, want: true},
		{name: "offset zero anchor", text: "a\nb", pattern: "^b", offset: 0, want: false},
		{name: "offset before match", text: "abc", pattern: "b", offset: 1, want: true},
		{name: "offset at match", text: "abc", pattern: "c", offset: 2, want: true},
		{name: "offset past match", text: "abc", pattern: "b", offset: 2, want: false},
		{name: "overlapping match", text: "aaa", pattern: "aa", offset: 1, want: true},
		{name: "word boundary preserved", text: "foobar", pattern: `\bbar`, offset: 3, want: false},
		{name: "offset equals length empty pattern", text: "abc", pattern: "", offset: 3, want: true},
		{name: "offset past length", text: "abc", pattern: "a", offset: 4, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := regexMatchFromRuneOffset("string.match?", tc.text, tc.pattern, tc.offset)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("regexMatchFromRuneOffset mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
