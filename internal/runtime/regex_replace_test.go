package runtime

import (
	"strings"
	"testing"
)

// TestStringRegexReplacementBackreferences exercises the Ruby-style replacement
// expansion shared by String#sub, String#sub!, String#gsub, and String#gsub!.
// Expected values were produced with Ruby 2.6.10 so the behavior stays aligned
// with MRI (\1/\& expand, $1/$& stay literal).
func TestStringRegexReplacementBackreferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "numbered groups swap",
			script: `def run() "abc123".sub("([a-z]+)([0-9]+)", "\\2-\\1", regex: true) end`,
			want:   "123-abc",
		},
		{
			name:   "dollar group is literal",
			script: `def run() "abc123".sub("([a-z]+)([0-9]+)", "$2-$1", regex: true) end`,
			want:   "$2-$1",
		},
		{
			name:   "ampersand expands whole match",
			script: `def run() "abc".sub("b", "<\\&>", regex: true) end`,
			want:   "a<b>c",
		},
		{
			name:   "dollar ampersand is literal",
			script: `def run() "abc".sub("b", "<$&>", regex: true) end`,
			want:   "a<$&>c",
		},
		{
			name:   "backslash zero expands whole match",
			script: `def run() "abc".sub("b", "<\\0>", regex: true) end`,
			want:   "a<b>c",
		},
		{
			name:   "gsub swaps groups on every match",
			script: `def run() "a1b2c3".gsub("([a-z])([0-9])", "\\2\\1", regex: true) end`,
			want:   "1a2b3c",
		},
		{
			name:   "gsub ampersand wraps every match",
			script: `def run() "cat dog".gsub("\\w+", "[\\&]", regex: true) end`,
			want:   "[cat] [dog]",
		},
		{
			name:   "prematch escape",
			script: "def run() \"abcdef\".sub(\"cd\", \"[\\\\`]\", regex: true) end",
			want:   "ab[ab]ef",
		},
		{
			name:   "postmatch escape",
			script: `def run() "abcdef".sub("cd", "[\\']", regex: true) end`,
			want:   "ab[ef]ef",
		},
		{
			name:   "last group escape",
			script: `def run() "abc123".sub("([a-z]+)([0-9]+)", "<\\+>", regex: true) end`,
			want:   "<123>",
		},
		{
			name:   "escaped backslash is literal",
			script: `def run() "abc".sub("b", "x\\\\y", regex: true) end`,
			want:   "ax\\yc",
		},
		{
			name:   "trailing backslash is literal",
			script: `def run() "abc".sub("b", "x\\", regex: true) end`,
			want:   "ax\\c",
		},
		{
			name:   "unknown escape keeps backslash",
			script: `def run() "abc".sub("b", "\\z", regex: true) end`,
			want:   "a\\zc",
		},
		{
			name:   "backslash n is literal not newline",
			script: `def run() "abc".sub("b", "\\n", regex: true) end`,
			want:   "a\\nc",
		},
		{
			name:   "single digit only on many groups",
			script: `def run() "0123456789X".gsub("(0)(1)(2)(3)(4)(5)(6)(7)(8)(9)(X)", "\\10", regex: true) end`,
			want:   "00",
		},
		{
			name:   "named groups",
			script: `def run() "John Smith".sub("(?<first>\\w+) (?<last>\\w+)", "\\k<last>, \\k<first>", regex: true) end`,
			want:   "Smith, John",
		},
		{
			name:   "out of range group is empty",
			script: `def run() "abc".sub("(b)", "\\2", regex: true) end`,
			want:   "ac",
		},
		{
			name:   "non-participating optional group is empty",
			script: `def run() "ac".sub("a(x)?c", "[\\1]", regex: true) end`,
			want:   "[]",
		},
		{
			name:   "k without bracket is literal",
			script: `def run() "abc".sub("b", "x\\ky", regex: true) end`,
			want:   "ax\\kyc",
		},
		{
			name:   "first match only for sub with global pattern",
			script: `def run() "ID-12 ID-34".sub("ID-([0-9]+)", "X-\\1", regex: true) end`,
			want:   "X-12 ID-34",
		},
		{
			name:   "sub bang applies ruby escapes",
			script: `def run() "abc123".sub!("([a-z]+)([0-9]+)", "\\2-\\1", regex: true) end`,
			want:   "123-abc",
		},
		{
			name:   "gsub bang applies ruby escapes",
			script: `def run() "a1b2".gsub!("([a-z])([0-9])", "\\2\\1", regex: true) end`,
			want:   "1a2b",
		},
		{
			name:   "non-regex sub keeps replacement literal",
			script: `def run() "a\\1b".sub("\\1", "X") end`,
			want:   "aXb",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			got := callFunc(t, script, "run", nil)
			if got.Kind() != KindString {
				t.Fatalf("expected string, got %v", got.Kind())
			}
			if got.String() != tc.want {
				t.Fatalf("result mismatch: got %q, want %q", got.String(), tc.want)
			}
		})
	}
}

// TestStringRegexReplacementErrors covers replacement templates that Ruby
// rejects: malformed named-group references.
func TestStringRegexReplacementErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "undefined named group",
			script: `def run() "John".sub("(?<f>\\w+)", "\\k<g>", regex: true) end`,
			want:   "undefined group name reference: g",
		},
		{
			name:   "unterminated named group",
			script: `def run() "John".sub("(?<f>\\w+)", "\\k<f", regex: true) end`,
			want:   "invalid group name reference format",
		},
		{
			name:   "undefined named group in gsub",
			script: `def run() "John Smith".gsub("(?<f>\\w+)", "\\k<bad>", regex: true) end`,
			want:   "undefined group name reference: bad",
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

// TestStringRegexReplacementOutputLimit verifies that the Ruby replacement
// expansion still enforces the shared regex output-size guard for both the
// first-match and replace-all paths.
func TestStringRegexReplacementOutputLimit(t *testing.T) {
	t.Parallel()

	re, err := compileCachedRegex("a")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// Each match expands "a" into a replacement that, repeated, overshoots the
	// limit, so expansion must stop with an output-limit error rather than
	// allocating an unbounded result.
	text := strings.Repeat("a", maxRegexInputBytes)
	replacement := strings.Repeat("x", 2)

	if _, err := rubyRegexGSub(re, text, replacement, "string.gsub"); err == nil {
		t.Fatalf("expected gsub output limit error, got nil")
	} else if !strings.Contains(err.Error(), "output exceeds limit") {
		t.Fatalf("unexpected gsub error: %v", err)
	}

	subText := "a" + strings.Repeat("b", maxRegexInputBytes-1)
	subReplacement := strings.Repeat("x", maxRegexInputBytes)
	if _, err := rubyRegexSub(re, subText, subReplacement, "string.sub"); err == nil {
		t.Fatalf("expected sub output limit error, got nil")
	} else if !strings.Contains(err.Error(), "output exceeds limit") {
		t.Fatalf("unexpected sub error: %v", err)
	}
}

// TestStringGsubEmptyMatchAdvances confirms the Ruby replacement path keeps the
// empty-match advancement behavior of the previous Go-expansion path so a
// zero-width pattern still terminates and inserts between every rune.
func TestStringGsubEmptyMatchAdvances(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run() "héllo".gsub("", "-", regex: true) end`)
	got := callFunc(t, script, "run", nil)
	want := "-h-é-l-l-o-"
	if got.String() != want {
		t.Fatalf("empty-match gsub mismatch: got %q, want %q", got.String(), want)
	}
}
