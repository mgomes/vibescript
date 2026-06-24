package runtime

import (
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
)

func TestStringHelpers(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      ["  hello  ".strip(), "hi".upcase(), "BYE".downcase(), "a b c".split()]
    end

    def split_custom()
      "a,b,c".split(",")
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindArray {
		t.Fatalf("expected array, got %v", result.Kind())
	}
	arr := result.Array()
	if len(arr) != 4 {
		t.Fatalf("unexpected length: %d", len(arr))
	}
	if arr[0].String() != "hello" {
		t.Fatalf("strip mismatch: %s", arr[0].String())
	}
	if arr[1].String() != "HI" {
		t.Fatalf("upcase mismatch: %s", arr[1].String())
	}
	if arr[2].String() != "bye" {
		t.Fatalf("downcase mismatch: %s", arr[2].String())
	}
	compareArrays(t, arr[3], []Value{NewString("a"), NewString("b"), NewString("c")})

	customSplit := callFunc(t, script, "split_custom", nil)
	compareArrays(t, customSplit, []Value{NewString("a"), NewString("b"), NewString("c")})
}

func TestSplitOnASCIIWhitespace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty", in: "", want: nil},
		{name: "blank ascii", in: " \t\n ", want: nil},
		{name: "single field", in: "hello", want: []string{"hello"}},
		{name: "ascii spaces", in: "a b c", want: []string{"a", "b", "c"}},
		{name: "mixed ascii whitespace", in: "a b\t c", want: []string{"a", "b", "c"}},
		{name: "leading trailing collapse", in: " a  b ", want: []string{"a", "b"}},
		{name: "tab", in: "a\tb", want: []string{"a", "b"}},
		{name: "newline", in: "a\nb", want: []string{"a", "b"}},
		{name: "vertical tab", in: "a\vb", want: []string{"a", "b"}},
		{name: "form feed", in: "a\fb", want: []string{"a", "b"}},
		{name: "carriage return", in: "a\rb", want: []string{"a", "b"}},
		{name: "crlf collapses", in: "a\r\nb", want: []string{"a", "b"}},
		// Wider Unicode whitespace stays inside the field, matching Ruby
		// rather than Go's strings.Fields Unicode table.
		{name: "nbsp kept", in: "a b", want: []string{"a b"}},
		{name: "em space kept", in: "a b", want: []string{"a b"}},
		{name: "en space kept", in: "a b", want: []string{"a b"}},
		{name: "thin space kept", in: "a b", want: []string{"a b"}},
		{name: "ideographic space kept", in: "a　b", want: []string{"a　b"}},
		{name: "ogham space kept", in: "a b", want: []string{"a b"}},
		{name: "zero width space kept", in: "a\u200bb", want: []string{"a\u200bb"}},
		{name: "next line kept", in: "a\u0085b", want: []string{"a\u0085b"}},
		{name: "line separator kept", in: "a b", want: []string{"a b"}},
		{name: "nbsp surrounded by ascii", in: " a b ", want: []string{"a b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := splitOnASCIIWhitespace(tt.in)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("splitOnASCIIWhitespace(%q) mismatch (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

func TestStringSplitDefaultWhitespace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// literal is spliced directly into the Vibescript double-quoted
		// string, so it must contain real bytes; the lexer only decodes
		// \n, \t, \", and \\ escapes.
		literal string
		want    []Value
	}{
		{name: "ascii", literal: "a b\t c", want: []Value{NewString("a"), NewString("b"), NewString("c")}},
		{name: "edges", literal: " a  b ", want: []Value{NewString("a"), NewString("b")}},
		{name: "nbsp kept", literal: "a b", want: []Value{NewString("a b")}},
		{name: "em space kept", literal: "a b", want: []Value{NewString("a b")}},
		{name: "blank yields empty", literal: "   ", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def go()\n  \""+tt.literal+"\".split()\nend\n")
			got := callFunc(t, script, "go", nil)
			if got.Kind() != KindArray {
				t.Fatalf("expected array, got %v", got.Kind())
			}
			if diff := valuesDiff(tt.want, got.Array()); diff != "" {
				t.Fatalf("split mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStringSplitNilSeparator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name:   "nil splits on whitespace",
			script: `def go() " a  b ".split(nil) end`,
			want:   []Value{NewString("a"), NewString("b")},
		},
		{
			name:   "nil matches no-argument form",
			script: `def go() "one two  three".split(nil) end`,
			want:   []Value{NewString("one"), NewString("two"), NewString("three")},
		},
		{
			name: "nil keeps wider unicode whitespace inside fields",
			// The non-breaking space (U+00A0) is spliced in via an explicit
			// escape so it cannot be confused with an ASCII space; the lexer
			// only decodes \n, \t, \", and \\ escapes, so the raw byte must
			// reach the runtime.
			script: "def go() \"a\u00a0b\".split(nil) end",
			want:   []Value{NewString("a\u00a0b")},
		},
		{
			name:   "nil on blank yields empty",
			script: `def go() "   ".split(nil) end`,
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.script)
			got := callFunc(t, script, "go", nil)
			if got.Kind() != KindArray {
				t.Fatalf("expected array, got %v", got.Kind())
			}
			if diff := valuesDiff(tt.want, got.Array()); diff != "" {
				t.Fatalf("split(nil) mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStringSplitRejectsInvalidSeparator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "integer separator",
			script: `def run() "a,b".split(1) end`,
			want:   "string.split separator must be string or nil",
		},
		{
			name:   "array separator",
			script: `def run() "a,b".split([","]) end`,
			want:   "string.split separator must be string or nil",
		},
		{
			name:   "too many separators",
			script: `def run() "a,b".split(",", "-") end`,
			want:   "string.split accepts at most one separator",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.script)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.want)
		})
	}
}

func TestStringPredicatesAndLength(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      {
        empty_true: "".empty?,
        empty_false: "hello".empty?,
        starts_true: "hello".start_with?("he"),
        starts_false: "hello".start_with?("lo"),
        ends_true: "hello".end_with?("lo"),
        ends_false: "hello".end_with?("he"),
        length_alias: "héllo".length,
        size: "héllo".size
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["empty_true"].Bool() {
		t.Fatalf("expected empty_true to be true")
	}
	if got["empty_false"].Bool() {
		t.Fatalf("expected empty_false to be false")
	}
	if !got["starts_true"].Bool() {
		t.Fatalf("expected starts_true to be true")
	}
	if got["starts_false"].Bool() {
		t.Fatalf("expected starts_false to be false")
	}
	if !got["ends_true"].Bool() {
		t.Fatalf("expected ends_true to be true")
	}
	if got["ends_false"].Bool() {
		t.Fatalf("expected ends_false to be false")
	}
	if got["length_alias"].Int() != 5 {
		t.Fatalf("length mismatch: %v", got["length_alias"])
	}
	if got["size"].Int() != 5 {
		t.Fatalf("size mismatch: %v", got["size"])
	}
}

func TestStringStartEndWithMultipleCandidates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   bool
	}{
		{
			name:   "start_with? any candidate matches",
			script: `def run() "hello".start_with?("x", "he") end`,
			want:   true,
		},
		{
			name:   "start_with? later candidate matches",
			script: `def run() "hello".start_with?("nope", "miss", "hell") end`,
			want:   true,
		},
		{
			name:   "start_with? no candidate matches",
			script: `def run() "hello".start_with?("x", "lo") end`,
			want:   false,
		},
		{
			name:   "start_with? single candidate matches",
			script: `def run() "hello".start_with?("he") end`,
			want:   true,
		},
		{
			name:   "start_with? match short-circuits before non-string",
			script: `def run() "hello".start_with?("he", 123) end`,
			want:   true,
		},
		{
			name:   "end_with? match short-circuits before non-string",
			script: `def run() "hello".end_with?("lo", 123) end`,
			want:   true,
		},
		{
			name:   "end_with? any candidate matches",
			script: `def run() "hello".end_with?("x", "lo") end`,
			want:   true,
		},
		{
			name:   "end_with? later candidate matches",
			script: `def run() "hello".end_with?("nope", "miss", "llo") end`,
			want:   true,
		},
		{
			name:   "end_with? no candidate matches",
			script: `def run() "hello".end_with?("x", "he") end`,
			want:   false,
		},
		{
			name:   "end_with? single candidate matches",
			script: `def run() "hello".end_with?("lo") end`,
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
				t.Fatalf("got %v, want %v", result.Bool(), tc.want)
			}
		})
	}
}

func TestStringStartEndWithErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "start_with? requires a prefix",
			script: `def run() "hello".start_with? end`,
			want:   "expects at least one prefix",
		},
		{
			name:   "end_with? requires a suffix",
			script: `def run() "hello".end_with? end`,
			want:   "expects at least one suffix",
		},
		{
			name:   "start_with? rejects non-string reached before a match",
			script: `def run() "hello".start_with?(123, "he") end`,
			want:   "prefix must be string",
		},
		{
			name:   "end_with? rejects non-string reached before a match",
			script: `def run() "hello".end_with?(123, "lo") end`,
			want:   "suffix must be string",
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

func TestStringBoundaryHelpers(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      {
        lstrip: "  hello\t".lstrip,
        rstrip: "\thello  ".rstrip,
        chomp_nl: "line\n".chomp,
        chomp_none: "line".chomp,
        chomp_custom: "path///".chomp("/"),
        chomp_empty_sep: "line\n\n".chomp(""),
        delete_prefix_hit: "unhappy".delete_prefix("un"),
        delete_prefix_miss: "happy".delete_prefix("un"),
        delete_suffix_hit: "report.csv".delete_suffix(".csv"),
        delete_suffix_miss: "report.csv".delete_suffix(".txt")
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	wantStrings := map[string]string{
		"lstrip":             "hello\t",
		"rstrip":             "\thello",
		"chomp_nl":           "line",
		"chomp_none":         "line",
		"chomp_custom":       "path//",
		"chomp_empty_sep":    "line",
		"delete_prefix_hit":  "happy",
		"delete_prefix_miss": "happy",
		"delete_suffix_hit":  "report",
		"delete_suffix_miss": "report.csv",
	}
	for key, want := range wantStrings {
		if got[key].String() != want {
			t.Fatalf("%s mismatch: %q, want %q", key, got[key].String(), want)
		}
	}
}

// TestStringChomp covers the immutable String#chomp across the default
// separator, an explicit separator, an empty separator, and Ruby's nil
// separator "do not chomp" case.
func TestStringChomp(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{name: "default newline", script: `def run() "line\n".chomp end`, want: "line"},
		{name: "default no separator", script: `def run() "line".chomp end`, want: "line"},
		{name: "explicit separator hit", script: `def run() "path///".chomp("/") end`, want: "path//"},
		{name: "explicit separator miss", script: `def run() "abc".chomp("/") end`, want: "abc"},
		{name: "empty separator strips newlines", script: `def run() "line\n\n".chomp("") end`, want: "line"},
		{name: "nil separator no chomp", script: `def run() "abc".chomp(nil) end`, want: "abc"},
		{name: "nil separator keeps newline", script: `def run() "abc\n".chomp(nil) end`, want: "abc\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString {
				t.Fatalf("expected string, got %v", result.Kind())
			}
			if result.String() != tc.want {
				t.Fatalf("chomp mismatch: %q, want %q", result.String(), tc.want)
			}
		})
	}
}

// TestStringChompBang covers the mutator String#chomp!: it returns the chomped
// value on change and nil when nothing changes, including Ruby's nil separator
// case which always returns nil because no chomp occurs.
func TestStringChompBang(t *testing.T) {
	t.Parallel()

	stringCases := []struct {
		name   string
		script string
		want   string
	}{
		{name: "default newline", script: `def run() "line\n".chomp! end`, want: "line"},
		{name: "explicit separator hit", script: `def run() "path/".chomp!("/") end`, want: "path"},
		{name: "empty separator strips newlines", script: `def run() "line\n\n".chomp!("") end`, want: "line"},
	}
	for _, tc := range stringCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString {
				t.Fatalf("expected string, got %v", result.Kind())
			}
			if result.String() != tc.want {
				t.Fatalf("chomp! mismatch: %q, want %q", result.String(), tc.want)
			}
		})
	}

	nilCases := []struct {
		name   string
		script string
	}{
		{name: "default no change", script: `def run() "line".chomp! end`},
		{name: "explicit separator miss", script: `def run() "abc".chomp!("/") end`},
		{name: "nil separator", script: `def run() "abc\n".chomp!(nil) end`},
		{name: "nil separator without newline", script: `def run() "abc".chomp!(nil) end`},
	}
	for _, tc := range nilCases {
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

func TestStringChompErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "chomp rejects extra arguments",
			script: `def run() "abc".chomp("a", "b") end`,
			want:   "string.chomp accepts at most one separator",
		},
		{
			name:   "chomp rejects non-string separator",
			script: `def run() "abc".chomp(1) end`,
			want:   "string.chomp separator must be string",
		},
		{
			name:   "chomp! rejects extra arguments",
			script: `def run() "abc".chomp!("a", "b") end`,
			want:   "string.chomp! accepts at most one separator",
		},
		{
			name:   "chomp! rejects non-string separator",
			script: `def run() "abc".chomp!(1) end`,
			want:   "string.chomp! separator must be string",
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

// TestChopDefault covers chopDefault directly so that record-separator cases
// using "\r" can be expressed: the Vibescript lexer recognizes only \n, \t,
// \" and \\ escapes, so a literal "\r" cannot be written in a script string.
func TestChopDefault(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "trailing newline", in: "abc\n", want: "abc"},
		{name: "plain string removes last char", in: "abc", want: "ab"},
		{name: "crlf removed together", in: "abc\r\n", want: "abc"},
		{name: "empty string unchanged", in: "", want: ""},
		{name: "single char to empty", in: "a", want: ""},
		{name: "lone carriage return removed", in: "abc\r", want: "abc"},
		{name: "unicode removes one rune", in: "héllo", want: "héll"},
		{name: "trailing multibyte rune", in: "café", want: "caf"},
		{name: "single multibyte rune to empty", in: "é", want: ""},
		{name: "lone newline to empty", in: "\n", want: ""},
		{name: "lone crlf to empty", in: "\r\n", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := chopDefault(tc.in); got != tc.want {
				t.Fatalf("chopDefault(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStringChop(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{name: "trailing newline", script: `def run() "abc\n".chop end`, want: "abc"},
		{name: "plain string removes last char", script: `def run() "abc".chop end`, want: "ab"},
		{name: "empty string unchanged", script: `def run() "".chop end`, want: ""},
		{name: "single char to empty", script: `def run() "a".chop end`, want: ""},
		{name: "unicode removes one rune", script: `def run() "héllo".chop end`, want: "héll"},
		{name: "trailing multibyte rune", script: `def run() "café".chop end`, want: "caf"},
		{name: "single multibyte rune to empty", script: `def run() "é".chop end`, want: ""},
		{name: "lone newline to empty", script: `def run() "\n".chop end`, want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString {
				t.Fatalf("expected string, got %v", result.Kind())
			}
			if result.String() != tc.want {
				t.Fatalf("chop mismatch: %q, want %q", result.String(), tc.want)
			}
		})
	}
}

func TestStringChopBang(t *testing.T) {
	t.Parallel()

	stringCases := []struct {
		name   string
		script string
		want   string
	}{
		{name: "removes last char", script: `def run() "abc".chop! end`, want: "ab"},
		{name: "trailing newline", script: `def run() "abc\n".chop! end`, want: "abc"},
		{name: "unicode removes one rune", script: `def run() "héllo".chop! end`, want: "héll"},
	}
	for _, tc := range stringCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString {
				t.Fatalf("expected string, got %v", result.Kind())
			}
			if result.String() != tc.want {
				t.Fatalf("chop! mismatch: %q, want %q", result.String(), tc.want)
			}
		})
	}

	nilCases := []struct {
		name   string
		script string
	}{
		{name: "empty string returns nil", script: `def run() "".chop! end`},
	}
	for _, tc := range nilCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindNil {
				t.Fatalf("expected nil, got %v", result.Kind())
			}
		})
	}

	t.Run("does not mutate the receiver", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `
			def run()
				original = "abc"
				chopped = original.chop!
				[original, chopped]
			end
		`)
		result := callFunc(t, script, "run", nil)
		if result.Kind() != KindArray {
			t.Fatalf("expected array, got %v", result.Kind())
		}
		elements := result.Array()
		if len(elements) != 2 {
			t.Fatalf("expected 2 elements, got %d", len(elements))
		}
		if got := elements[0].String(); got != "abc" {
			t.Fatalf("chop! mutated the receiver: %q, want %q", got, "abc")
		}
		if got := elements[1].String(); got != "ab" {
			t.Fatalf("chop! returned %q, want %q", got, "ab")
		}
	})
}

func TestStringChopErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "chop rejects arguments",
			script: `def run() "abc".chop("x") end`,
			want:   "string.chop does not take arguments",
		},
		{
			name:   "chop! rejects arguments",
			script: `def run() "abc".chop!("x") end`,
			want:   "string.chop! does not take arguments",
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

func TestStringChr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{name: "first ascii char", script: `def run() "abc".chr end`, want: "a"},
		{name: "single char", script: `def run() "a".chr end`, want: "a"},
		{name: "leading multibyte rune", script: `def run() "éxy".chr end`, want: "é"},
		{name: "empty string returns empty string", script: `def run() "".chr end`, want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString {
				t.Fatalf("expected string, got %v", result.Kind())
			}
			if result.String() != tc.want {
				t.Fatalf("chr mismatch: %q, want %q", result.String(), tc.want)
			}
		})
	}

	t.Run("rejects arguments", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, `def run() "abc".chr("x") end`)
		requireCallErrorContains(t, script, "run", nil, CallOptions{}, "string.chr does not take arguments")
	})
}

func TestStringSearchAndSlice(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      text = "héllo hello"
      {
        include_true: text.include?("llo"),
        include_false: text.include?("zzz"),
        index_hit: text.index("llo"),
        index_offset_hit: text.index("llo", 6),
        index_miss: text.index("zzz"),
        index_empty: text.index("", 1),
        index_empty_end: text.index("", 11),
        index_empty_oob: text.index("", 12),
        rindex_hit: text.rindex("llo"),
        rindex_offset_hit: text.rindex("llo", 4),
        rindex_miss: text.rindex("zzz"),
        rindex_empty: text.rindex(""),
        rindex_empty_offset: text.rindex("", 4),
        rindex_empty_oob: text.rindex("", 99),
        slice_char: text.slice(1),
        slice_range: text.slice(1, 4),
        slice_zero_len: text.slice(1, 0),
        slice_oob: text.slice(99),
        slice_negative_len: text.slice(1, -1),
        slice_huge_len: text.slice(1, 9223372036854775807)
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["include_true"].Bool() {
		t.Fatalf("include_true mismatch")
	}
	if got["include_false"].Bool() {
		t.Fatalf("include_false mismatch")
	}
	if got["index_hit"].Int() != 2 {
		t.Fatalf("index_hit mismatch: %v", got["index_hit"])
	}
	if got["index_offset_hit"].Int() != 8 {
		t.Fatalf("index_offset_hit mismatch: %v", got["index_offset_hit"])
	}
	if got["index_miss"].Kind() != KindNil {
		t.Fatalf("index_miss expected nil, got %v", got["index_miss"])
	}
	if got["index_empty"].Int() != 1 || got["index_empty_end"].Int() != 11 {
		t.Fatalf("index_empty mismatch: %v end=%v", got["index_empty"], got["index_empty_end"])
	}
	if got["index_empty_oob"].Kind() != KindNil {
		t.Fatalf("index_empty_oob expected nil, got %v", got["index_empty_oob"])
	}
	if got["rindex_hit"].Int() != 8 {
		t.Fatalf("rindex_hit mismatch: %v", got["rindex_hit"])
	}
	if got["rindex_offset_hit"].Int() != 2 {
		t.Fatalf("rindex_offset_hit mismatch: %v", got["rindex_offset_hit"])
	}
	if got["rindex_miss"].Kind() != KindNil {
		t.Fatalf("rindex_miss expected nil, got %v", got["rindex_miss"])
	}
	if got["rindex_empty"].Int() != 11 || got["rindex_empty_offset"].Int() != 4 || got["rindex_empty_oob"].Int() != 11 {
		t.Fatalf("rindex_empty mismatch: default=%v offset=%v oob=%v", got["rindex_empty"], got["rindex_empty_offset"], got["rindex_empty_oob"])
	}
	if got["slice_char"].String() != "é" {
		t.Fatalf("slice_char mismatch: %q", got["slice_char"].String())
	}
	if got["slice_range"].String() != "éllo" {
		t.Fatalf("slice_range mismatch: %q", got["slice_range"].String())
	}
	if got["slice_zero_len"].String() != "" {
		t.Fatalf("slice_zero_len mismatch: %q", got["slice_zero_len"].String())
	}
	if got["slice_oob"].Kind() != KindNil {
		t.Fatalf("slice_oob expected nil, got %v", got["slice_oob"])
	}
	if got["slice_negative_len"].Kind() != KindNil {
		t.Fatalf("slice_negative_len expected nil, got %v", got["slice_negative_len"])
	}
	if got["slice_huge_len"].String() != "éllo hello" {
		t.Fatalf("slice_huge_len mismatch: %q", got["slice_huge_len"].String())
	}
}

func TestStringIndexNegativeOffset(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def find_index(text, needle, offset)
      text.index(needle, offset)
    end

    def find_rindex(text, needle, offset)
      text.rindex(needle, offset)
    end
    `)

	// Expectations cross-checked against Ruby 2.6.10 (see issue #615).
	tests := []struct {
		name    string
		fn      string
		text    string
		needle  string
		offset  int64
		wantNil bool
		want    int64
	}{
		{name: "index negative within range", fn: "find_index", text: "hello", needle: "l", offset: -3, want: 2},
		{name: "index negative maps to start", fn: "find_index", text: "hello", needle: "l", offset: -5, want: 2},
		{name: "index negative before start", fn: "find_index", text: "hello", needle: "l", offset: -9, wantNil: true},
		{name: "index negative last rune", fn: "find_index", text: "hello", needle: "e", offset: -1, wantNil: true},
		{name: "index negative empty needle", fn: "find_index", text: "hello", needle: "", offset: -2, want: 3},
		{name: "index negative empty needle start", fn: "find_index", text: "hello", needle: "", offset: -5, want: 0},
		{name: "index negative empty needle before start", fn: "find_index", text: "hello", needle: "", offset: -6, wantNil: true},
		{name: "index negative needle past offset", fn: "find_index", text: "hello", needle: "lo", offset: -1, wantNil: true},
		{name: "index negative multibyte", fn: "find_index", text: "héllo", needle: "l", offset: -3, want: 2},
		{name: "rindex negative within range", fn: "find_rindex", text: "hello", needle: "l", offset: -2, want: 3},
		{name: "rindex negative before start", fn: "find_rindex", text: "hello", needle: "l", offset: -9, wantNil: true},
		{name: "rindex negative last rune", fn: "find_rindex", text: "hello", needle: "l", offset: -1, want: 3},
		{name: "rindex negative empty needle", fn: "find_rindex", text: "hello", needle: "", offset: -2, want: 3},
		{name: "rindex negative empty needle before start", fn: "find_rindex", text: "hello", needle: "", offset: -6, wantNil: true},
		{name: "rindex negative needle ends at offset", fn: "find_rindex", text: "hello", needle: "lo", offset: -1, want: 3},
		{name: "rindex negative multibyte", fn: "find_rindex", text: "héllo", needle: "l", offset: -2, want: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := []Value{NewString(tc.text), NewString(tc.needle), NewInt(tc.offset)}
			got := callFunc(t, script, tc.fn, args)
			if tc.wantNil {
				if got.Kind() != KindNil {
					t.Fatalf("%s(%q, %q, %d) = %v, want nil", tc.fn, tc.text, tc.needle, tc.offset, got)
				}
				return
			}
			if got.Kind() != KindInt || got.Int() != tc.want {
				t.Fatalf("%s(%q, %q, %d) = %v, want %d", tc.fn, tc.text, tc.needle, tc.offset, got, tc.want)
			}
		})
	}
}

func TestStringSliceNormalizesInvalidUTF8(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def first_char(text)
      text.slice(0, 1)
    end
    `)

	result := callFunc(t, script, "first_char", []Value{NewString("\xff")})
	if got := result.String(); got != "\uFFFD" {
		t.Fatalf("first_char invalid UTF-8 = %q, want replacement rune", got)
	}
	if !utf8.ValidString(result.String()) {
		t.Fatalf("first_char invalid UTF-8 returned non-UTF-8 string %q", result.String())
	}
}

// TestStringSliceRubySemantics verifies String#slice across the argument shapes
// Vibescript can represent, mirroring Ruby 2.6.10. wantNil marks an
// out-of-range selector that must return nil; otherwise want is the expected
// substring.
func TestStringSliceRubySemantics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		expr    string
		want    string
		wantNil bool
	}{
		{name: "positive index", expr: `"héllo".slice(0)`, want: "h"},
		{name: "positive index interior", expr: `"héllo".slice(1)`, want: "é"},
		{name: "negative index", expr: `"héllo".slice(-1)`, want: "o"},
		{name: "negative index to first", expr: `"héllo".slice(-5)`, want: "h"},
		{name: "negative index out of range", expr: `"héllo".slice(-6)`, wantNil: true},
		{name: "index at length is nil", expr: `"héllo".slice(5)`, wantNil: true},
		{name: "index past length is nil", expr: `"héllo".slice(6)`, wantNil: true},
		{name: "float index truncates", expr: `"héllo".slice(1.9)`, want: "é"},
		{name: "negative float index truncates", expr: `"héllo".slice(-1.5)`, want: "o"},

		{name: "start and length", expr: `"héllo".slice(1, 4)`, want: "éllo"},
		{name: "negative start and length", expr: `"héllo".slice(-3, 2)`, want: "ll"},
		{name: "zero length", expr: `"héllo".slice(1, 0)`, want: ""},
		{name: "start at length zero len", expr: `"héllo".slice(5, 0)`, want: ""},
		{name: "start at length positive len", expr: `"héllo".slice(5, 2)`, want: ""},
		{name: "start past length", expr: `"héllo".slice(6, 1)`, wantNil: true},
		{name: "negative length is nil", expr: `"héllo".slice(1, -1)`, wantNil: true},
		{name: "negative start positive length", expr: `"héllo".slice(-1, 2)`, want: "o"},
		{name: "huge length clamps to end", expr: `"héllo".slice(1, 9223372036854775807)`, want: "éllo"},

		{name: "inclusive range to end", expr: `"héllo".slice(1..-1)`, want: "éllo"},
		{name: "exclusive range", expr: `"héllo".slice(1...3)`, want: "él"},
		{name: "inclusive range", expr: `"héllo".slice(1..2)`, want: "él"},
		{name: "negative inclusive range", expr: `"héllo".slice(-3..-1)`, want: "llo"},
		{name: "negative exclusive range", expr: `"héllo".slice(-3...-1)`, want: "ll"},
		{name: "whole range", expr: `"héllo".slice(0..-1)`, want: "héllo"},
		{name: "range begin at length", expr: `"héllo".slice(5..7)`, want: ""},
		{name: "range begin past length", expr: `"héllo".slice(6..7)`, wantNil: true},
		{name: "range begin too negative", expr: `"héllo".slice(-6..-1)`, wantNil: true},
		{name: "range begin after end", expr: `"héllo".slice(2..1)`, want: ""},
		{name: "range mixed bounds", expr: `"héllo".slice(1..-3)`, want: "él"},

		{name: "substring present", expr: `"héllo".slice("llo")`, want: "llo"},
		{name: "substring multibyte", expr: `"héllo".slice("é")`, want: "é"},
		{name: "substring absent", expr: `"héllo".slice("x")`, wantNil: true},
		{name: "empty substring", expr: `"héllo".slice("")`, want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			got := callFunc(t, script, "run", nil)
			if tc.wantNil {
				if got.Kind() != KindNil {
					t.Fatalf("%s = %v, want nil", tc.expr, got)
				}
				return
			}
			if got.Kind() != KindString {
				t.Fatalf("%s kind = %v, want string", tc.expr, got.Kind())
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.expr, got.String(), tc.want)
			}
		})
	}
}

func TestStringSliceErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "no arguments",
			script: `def run() "abc".slice() end`,
			want:   "string.slice expects an index, range, or substring with optional length",
		},
		{
			name:   "too many arguments",
			script: `def run() "abc".slice(1, 2, 3) end`,
			want:   "string.slice expects an index, range, or substring with optional length",
		},
		{
			name:   "non-numeric single argument",
			script: `def run() "abc".slice(true) end`,
			want:   "string.slice index must be an integer, range, or substring",
		},
		{
			name:   "nil single argument",
			script: `def run() "abc".slice(nil) end`,
			want:   "string.slice index must be an integer, range, or substring",
		},
		{
			name:   "substring with length",
			script: `def run() "abc".slice("a", 2) end`,
			want:   "string.slice index must be integer",
		},
		{
			name:   "non-numeric length",
			script: `def run() "abc".slice(0, "x") end`,
			want:   "string.slice length must be integer",
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

func TestStringTransforms(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      original = "  hello  "
      ids = "ID-12 ID-34"
      template_context = {
        user: { name: "Alex", score: 42 },
        id: :p_1,
        missing_nil: nil
      }
      {
        bytesize: "hé".bytesize,
        ord: "hé".ord,
        chr: "hé".chr,
        chr_empty: "".chr,
        capitalize: "hÉLLo wORLD".capitalize,
        capitalize_bang: "hÉLLo wORLD".capitalize!,
        capitalize_bang_nochange: "Hello".capitalize!,
        swapcase: "Hello VIBE".swapcase,
        swapcase_bang: "Hello VIBE".swapcase!,
        upcase_bang_nochange: "HELLO".upcase!,
        reverse: "héllo".reverse,
        reverse_bang: "héllo".reverse!,
        sub_one: "bananas".sub("na", "NA"),
        sub_bang: "bananas".sub!("na", "NA"),
        sub_bang_nochange: "bananas".sub!("zz", "NA"),
        sub_miss: "bananas".sub("zz", "NA"),
        sub_regex: ids.sub("ID-[0-9]+", "X", regex: true),
        sub_regex_capture: ids.sub("ID-([0-9]+)", "X-$1", regex: true),
        sub_regex_boundary_short: "ba".sub("\\Ba", "X", regex: true),
        sub_regex_boundary: "foo".sub("\\Boo", "X", regex: true),
        sub_regex_boundary_full: "xfooy".sub("\\Bfoo\\B", "X", regex: true),
        gsub_all: "bananas".gsub("na", "NA"),
        gsub_bang: "bananas".gsub!("na", "NA"),
        gsub_bang_nochange: "bananas".gsub!("zz", "NA"),
        gsub_regex: ids.gsub("ID-[0-9]+", "X", regex: true),
        match: ids.match("ID-([0-9]+)"),
        match_optional_nil: "ID".match("(ID)(-([0-9]+))?"),
        match_miss: ids.match("ZZZ"),
        scan: ids.scan("ID-[0-9]+"),
        clear: "hello".clear,
        concat: "he".concat("llo", "!"),
        concat_noop: "hello".concat,
        replace: "old".replace("new"),
        strip_bang: original.strip!,
        strip_bang_nochange: "hello".strip!,
        squish: "  hello \n\t world  ".squish,
        squish_bang: "  hello \n\t world  ".squish!,
        squish_bang_nochange: "hello world".squish!,
        template_basic: "Hello {{name}}".template({ name: "Alex" }),
        template_nested: "Player {{user.name}} scored {{user.score}}".template(template_context),
        template_symbol: "ID={{id}}".template(template_context),
        template_nil: "Value={{missing_nil}}".template(template_context),
        template_missing_passthrough: "Hello {{missing}}".template({ name: "Alex" }),
        template_spacing: "Hello {{ name }}".template({ name: "Alex" }),
        template_multiple: "{{name}}/{{name}}".template({ name: "Alex" }),
        original_unchanged: original
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if got["bytesize"].Int() != 3 {
		t.Fatalf("bytesize mismatch: %v", got["bytesize"])
	}
	if got["ord"].Int() != 104 {
		t.Fatalf("ord mismatch: %v", got["ord"])
	}
	if got["chr"].String() != "h" {
		t.Fatalf("chr mismatch: %q", got["chr"].String())
	}
	if got["chr_empty"].Kind() != KindString || got["chr_empty"].String() != "" {
		t.Fatalf("chr_empty expected empty string, got %v", got["chr_empty"])
	}

	stringChecks := map[string]string{
		"capitalize":                   "Héllo world",
		"capitalize_bang":              "Héllo world",
		"swapcase":                     "hELLO vibe",
		"swapcase_bang":                "hELLO vibe",
		"reverse":                      "olléh",
		"reverse_bang":                 "olléh",
		"sub_one":                      "baNAnas",
		"sub_bang":                     "baNAnas",
		"sub_miss":                     "bananas",
		"sub_regex":                    "X ID-34",
		"sub_regex_capture":            "X-12 ID-34",
		"sub_regex_boundary_short":     "bX",
		"sub_regex_boundary":           "fX",
		"sub_regex_boundary_full":      "xXy",
		"gsub_all":                     "baNANAs",
		"gsub_bang":                    "baNANAs",
		"gsub_regex":                   "X X",
		"clear":                        "",
		"concat":                       "hello!",
		"concat_noop":                  "hello",
		"replace":                      "new",
		"strip_bang":                   "hello",
		"squish":                       "hello world",
		"squish_bang":                  "hello world",
		"template_basic":               "Hello Alex",
		"template_nested":              "Player Alex scored 42",
		"template_symbol":              "ID=p_1",
		"template_nil":                 "Value=",
		"template_missing_passthrough": "Hello {{missing}}",
		"template_spacing":             "Hello Alex",
		"template_multiple":            "Alex/Alex",
		"original_unchanged":           "  hello  ",
	}
	for key, want := range stringChecks {
		if got[key].String() != want {
			t.Fatalf("%s mismatch: %q, want %q", key, got[key].String(), want)
		}
	}

	nilChecks := []string{
		"capitalize_bang_nochange",
		"upcase_bang_nochange",
		"sub_bang_nochange",
		"gsub_bang_nochange",
		"strip_bang_nochange",
		"squish_bang_nochange",
	}
	for _, key := range nilChecks {
		if got[key].Kind() != KindNil {
			t.Fatalf("%s expected nil, got %v", key, got[key])
		}
	}

	compareArrays(t, got["match"], []Value{NewString("ID-12"), NewString("12")})
	compareArrays(t, got["match_optional_nil"], []Value{NewString("ID"), NewString("ID"), NewNil(), NewNil()})
	if got["match_miss"].Kind() != KindNil {
		t.Fatalf("match_miss expected nil, got %v", got["match_miss"])
	}
	compareArrays(t, got["scan"], []Value{NewString("ID-12"), NewString("ID-34")})
}
