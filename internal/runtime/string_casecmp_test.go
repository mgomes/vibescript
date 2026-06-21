package runtime

import "testing"

func TestStringCasecmp(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   int64
	}{
		{
			name:   "equal ignoring case",
			script: `def run() "abc".casecmp("ABC") end`,
			want:   0,
		},
		{
			name:   "less than",
			script: `def run() "abc".casecmp("ABD") end`,
			want:   -1,
		},
		{
			name:   "greater than",
			script: `def run() "abd".casecmp("ABC") end`,
			want:   1,
		},
		{
			name:   "prefix is less than longer string",
			script: `def run() "abc".casecmp("abcd") end`,
			want:   -1,
		},
		{
			name:   "empty strings are equal",
			script: `def run() "".casecmp("") end`,
			want:   0,
		},
		{
			name:   "empty is less than non-empty",
			script: `def run() "".casecmp("a") end`,
			want:   -1,
		},
		{
			name:   "non-empty is greater than empty",
			script: `def run() "a".casecmp("") end`,
			want:   1,
		},
		{
			name:   "non-ascii bytes compare ordinally below ascii fold",
			script: `def run() "ä".casecmp("Ä") end`,
			want:   1,
		},
		{
			name:   "non-ascii bytes compare ordinally above ascii fold",
			script: `def run() "Ä".casecmp("ä") end`,
			want:   -1,
		},
		{
			// Ruby folds 'A' down to 'a' (97) before comparing, so the
			// bracket byte (91) sorts below the folded letter.
			name:   "uppercase letter folds below punctuation bracket",
			script: `def run() "[".casecmp("A") end`,
			want:   -1,
		},
		{
			// 'z' (122) keeps its value and sorts above the bracket (91).
			name:   "lowercase letter orders above punctuation bracket",
			script: `def run() "z".casecmp("[") end`,
			want:   1,
		},
		{
			// 'A' folds to 'a' (97), which sorts above the underscore byte
			// (95) sitting between 'Z' and 'a'. Folding upward instead would
			// have placed 'A' (65) below the underscore and inverted this.
			name:   "uppercase letter folds above underscore",
			script: `def run() "A".casecmp("_") end`,
			want:   1,
		},
		{
			// Backtick (96) sits just below 'a'; the folded letter wins.
			name:   "uppercase letter folds above backtick",
			script: `def run() "B".casecmp("` + "`" + `") end`,
			want:   1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindInt {
				t.Fatalf("expected int, got %v", result.Kind())
			}
			if result.Int() != tc.want {
				t.Fatalf("casecmp = %d, want %d", result.Int(), tc.want)
			}
		})
	}
}

func TestStringCasecmpNonString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
	}{
		{
			name:   "integer argument yields nil",
			script: `def run() "abc".casecmp(1) end`,
		},
		{
			name:   "nil argument yields nil",
			script: `def run() "abc".casecmp(nil) end`,
		},
		{
			name:   "array argument yields nil",
			script: `def run() "abc".casecmp(["abc"]) end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindNil {
				t.Fatalf("expected nil, got %v", result.Kind())
			}
		})
	}
}

func TestStringCasecmpPredicate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   bool
	}{
		{
			name:   "equal ignoring case",
			script: `def run() "abc".casecmp?("ABC") end`,
			want:   true,
		},
		{
			name:   "different content",
			script: `def run() "abc".casecmp?("ABD") end`,
			want:   false,
		},
		{
			name:   "different length",
			script: `def run() "abc".casecmp?("abcd") end`,
			want:   false,
		},
		{
			name:   "empty strings are equal",
			script: `def run() "".casecmp?("") end`,
			want:   true,
		},
		{
			name:   "empty differs from non-empty",
			script: `def run() "".casecmp?("a") end`,
			want:   false,
		},
		{
			name:   "unicode simple case folding matches",
			script: `def run() "héllo".casecmp?("HÉLLO") end`,
			want:   true,
		},
		{
			name:   "unicode accent folding matches",
			script: `def run() "ä".casecmp?("Ä") end`,
			want:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindBool {
				t.Fatalf("expected bool, got %v", result.Kind())
			}
			if result.Bool() != tc.want {
				t.Fatalf("casecmp? = %v, want %v", result.Bool(), tc.want)
			}
		})
	}
}

func TestStringCasecmpPredicateNonString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
	}{
		{
			name:   "integer argument yields nil",
			script: `def run() "abc".casecmp?(1) end`,
		},
		{
			name:   "nil argument yields nil",
			script: `def run() "abc".casecmp?(nil) end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindNil {
				t.Fatalf("expected nil, got %v", result.Kind())
			}
		})
	}
}

func TestStringCasecmpArityErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "casecmp without argument",
			script: `def run() "abc".casecmp end`,
			want:   "casecmp expects exactly one string",
		},
		{
			name:   "casecmp with two arguments",
			script: `def run() "abc".casecmp("a", "b") end`,
			want:   "casecmp expects exactly one string",
		},
		{
			name:   "casecmp? without argument",
			script: `def run() "abc".casecmp? end`,
			want:   "casecmp? expects exactly one string",
		},
		{
			name:   "casecmp? with two arguments",
			script: `def run() "abc".casecmp?("a", "b") end`,
			want:   "casecmp? expects exactly one string",
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

func TestAsciiCaseCompare(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{name: "equal_ascii_fold", a: "AbC", b: "aBc", want: 0},
		{name: "less_than", a: "abc", b: "abd", want: -1},
		{name: "greater_than", a: "abd", b: "abc", want: 1},
		{name: "prefix_less", a: "abc", b: "abcd", want: -1},
		{name: "empty_equal", a: "", b: "", want: 0},
		{name: "empty_less", a: "", b: "a", want: -1},
		{name: "uppercase_folds_below_bracket", a: "[", b: "A", want: -1},
		{name: "lowercase_above_bracket", a: "z", b: "[", want: 1},
		{name: "uppercase_folds_above_underscore", a: "A", b: "_", want: 1},
		{name: "uppercase_folds_above_backtick", a: "B", b: "`", want: 1},
		{name: "non_ascii_ordinal", a: "ä", b: "Ä", want: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := asciiCaseCompare(tc.a, tc.b); got != tc.want {
				t.Fatalf("asciiCaseCompare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
