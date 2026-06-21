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
			// Ruby folds via ASCII TOLOWER, so 'a' stays at 97 and sorts above
			// the bracket byte (91) sitting between 'Z' and 'a'.
			name:   "lowercase letter orders above punctuation bracket",
			script: `def run() "a".casecmp("[") end`,
			want:   1,
		},
		{
			// 'A' folds down to 'a' (97), which sorts above the bracket (91).
			// Folding upward would have left 'A' at 65 and inverted this.
			name:   "uppercase letter folds above punctuation bracket",
			script: `def run() "A".casecmp("[") end`,
			want:   1,
		},
		{
			// Ruby returns -1 here: the lone bracket (91) folds to itself and
			// sorts below 'A' folded down to 'a' (97). This is the case the
			// Codex review flagged.
			name:   "punctuation bracket orders below folded uppercase letter",
			script: `def run() "[".casecmp("A") end`,
			want:   -1,
		},
		{
			// 'a' stays at 97 and sorts above the underscore byte (95) sitting
			// between 'Z' and 'a'. Folding upward to 'A' (65) would have placed
			// the letter below the underscore and inverted this.
			name:   "lowercase letter orders above underscore",
			script: `def run() "a".casecmp("_") end`,
			want:   1,
		},
		{
			// Backtick (96) sits just below 'a'; the letter (97, unchanged by
			// downward folding) wins against the punctuation byte.
			name:   "lowercase letter orders above backtick",
			script: `def run() "b".casecmp("` + "`" + `") end`,
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

// TestStringCasecmpPredicateInvalidUTF8 guards the byte-identity contract for
// casecmp? when operands carry invalid UTF-8. Host-provided strings (via
// NewString or capability values) can hold raw bytes that are not valid UTF-8,
// and strings.EqualFold would decode every invalid byte as utf8.RuneError,
// reporting distinct sequences such as "\xff" and "\xfe" as equal. casecmp?
// must instead fold byte-wise over ASCII letters so that byte identity is
// preserved, matching Ruby's binary-string path.
func TestStringCasecmpPredicateInvalidUTF8(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run(a, b) a.casecmp?(b) end`)

	cases := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{name: "distinct invalid bytes differ", a: "\xff", b: "\xfe", want: false},
		{name: "identical invalid bytes match", a: "\xff", b: "\xff", want: true},
		{name: "ascii fold around shared invalid byte", a: "A\xff", b: "a\xff", want: true},
		{name: "ascii fold with differing invalid byte", a: "a\xff", b: "A\xfe", want: false},
		{name: "ascii prefix versus invalid trailing byte", a: "abc", b: "ab\xff", want: false},
		{name: "invalid receiver versus valid argument", a: "\xff", b: "abc", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := callFunc(t, script, "run", []Value{NewString(tc.a), NewString(tc.b)})
			if result.Kind() != KindBool {
				t.Fatalf("expected bool, got %v", result.Kind())
			}
			if result.Bool() != tc.want {
				t.Fatalf("casecmp?(%q, %q) = %v, want %v", tc.a, tc.b, result.Bool(), tc.want)
			}
		})
	}
}

func TestCaseInsensitiveEqual(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{name: "ascii_fold", a: "AbC", b: "aBc", want: true},
		{name: "ascii_differ", a: "abc", b: "abd", want: false},
		{name: "length_differs", a: "abc", b: "abcd", want: false},
		{name: "empty_equal", a: "", b: "", want: true},
		{name: "unicode_simple_fold", a: "héllo", b: "HÉLLO", want: true},
		{name: "unicode_accent_fold", a: "ä", b: "Ä", want: true},
		{name: "simple_fold_excludes_full_fold", a: "ß", b: "SS", want: false},
		{name: "invalid_bytes_distinct", a: "\xff", b: "\xfe", want: false},
		{name: "invalid_bytes_identical", a: "\xff", b: "\xff", want: true},
		{name: "invalid_byte_with_ascii_fold", a: "A\xff", b: "a\xff", want: true},
		{name: "invalid_byte_differs", a: "a\xff", b: "A\xfe", want: false},
		{name: "valid_versus_invalid", a: "abc", b: "ab\xff", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := caseInsensitiveEqual(tc.a, tc.b); got != tc.want {
				t.Fatalf("caseInsensitiveEqual(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
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
		{name: "bracket_below_folded_letter", a: "[", b: "A", want: -1},
		{name: "folded_letter_above_bracket", a: "z", b: "[", want: 1},
		{name: "folded_letter_above_underscore", a: "A", b: "_", want: 1},
		{name: "folded_letter_above_backtick", a: "B", b: "`", want: 1},
		{name: "uppercase_folds_above_bracket", a: "A", b: "[", want: 1},
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
