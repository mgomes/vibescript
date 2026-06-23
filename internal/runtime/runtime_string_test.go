package runtime

import (
	"testing"
	"unicode/utf8"
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
