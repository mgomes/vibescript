package runtime

import (
	"strings"
	"testing"
)

// TestRegexLiteralBehavior pins Ruby-compatible semantics for /pattern/flags
// literals, the =~ and !~ match operators, and regex case equality.
func TestRegexLiteralBehavior(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def match_index()
      "abc" =~ /b/
    end

    def match_miss()
      "abc" =~ /z/
    end

    def match_reversed()
      /b/ =~ "abc"
    end

    def match_rune_index()
      "héllo" =~ /l/
    end

    def not_match_miss()
      "abc" !~ /z/
    end

    def not_match_hit()
      "abc" !~ /b/
    end

    def flag_case_insensitive()
      "ABC" =~ /b/i
    end

    def flag_dot_newline()
      "a\nb" =~ /a.b/m
    end

    def literal_match_captures()
      m = /o(b)a/.match("foobar")
      [m[0], m[1], m.captures, m.pre_match, m.post_match]
    end

    def literal_match_miss()
      /z/.match("foobar")
    end

    def literal_match_predicate()
      [/b/.match?("abc"), /z/.match?("abc")]
    end

    def literal_source_and_flags()
      re = /a[0-9]+/mi
      [re.source, re.flags]
    end

    def equality()
      [/a/ == /a/, /a/ == /a/i, /a/ == /b/, /a/im == /a/mi]
    end

    def case_equality()
      /ell/ === "hello"
    end

    def case_equality_non_string()
      /1/ === 1
    end

    def when_matching(text)
      case text
      when /^[0-9]+$/
        :number
      when /^[a-z]+$/
        :word
      else
        :other
      end
    end

    def inspect_form()
      /b+/i.inspect
    end

    def interpolation_form()
      re = /b+/i
      "#{re}"
    end

    def membership()
      [/a/, /b/].include?(/a/)
    end

    def unique()
      [/a/, /a/, /b/].uniq.size
    end

    def regexp_new_kind()
      re = Regexp.new("ab+")
      [re.source, re.match("cabbb")[0], re === "xabx"]
    end

    def regexp_union_matches()
      Regexp.union("cat", "dog") === "hotdog"
    end

    def string_match_regex()
      "foobar".match(/o(b)a/)[1]
    end

    def string_match_predicate_regex()
      "foobar".match?(/ba./)
    end

    def string_scan_regex()
      "a1 b2 c3".scan(/[a-z](\d)/)
    end

    def string_gsub_regex()
      "ID-12 ID-34".gsub(/ID-(\d+)/, "N\\1")
    end

    def string_sub_regex_block()
      "hello world".sub(/o/) do |m|
        m.upcase
      end
    end

    def string_sub_literal_stays_literal()
      "a.c".sub(".", "!")
    end

    def task_boundary(re)
      Tasks.map(["cabbb"], with: :task_probe)[0]
    end

    def task_probe(text)
      text =~ /b+/
    end
    `)

	sym := func(name string) Value { return NewSymbol(name) }

	tests := []struct {
		name string
		fn   string
		args []Value
		want Value
	}{
		{name: "match_index", fn: "match_index", want: NewInt(1)},
		{name: "match_miss_is_nil", fn: "match_miss", want: NewNil()},
		{name: "match_reversed_operands", fn: "match_reversed", want: NewInt(1)},
		{name: "match_returns_rune_index", fn: "match_rune_index", want: NewInt(2)},
		{name: "not_match_miss", fn: "not_match_miss", want: NewBool(true)},
		{name: "not_match_hit", fn: "not_match_hit", want: NewBool(false)},
		{name: "flag_i", fn: "flag_case_insensitive", want: NewInt(1)},
		{name: "flag_m_dot_matches_newline", fn: "flag_dot_newline", want: NewInt(0)},
		{
			name: "literal_match_captures",
			fn:   "literal_match_captures",
			want: NewArray([]Value{
				NewString("oba"),
				NewString("b"),
				NewArray([]Value{NewString("b")}),
				NewString("fo"),
				NewString("r"),
			}),
		},
		{name: "literal_match_miss", fn: "literal_match_miss", want: NewNil()},
		{
			name: "literal_match_predicate",
			fn:   "literal_match_predicate",
			want: NewArray([]Value{NewBool(true), NewBool(false)}),
		},
		{
			name: "source_and_flags",
			fn:   "literal_source_and_flags",
			want: NewArray([]Value{NewString("a[0-9]+"), NewString("im")}),
		},
		{
			name: "equality_by_source_and_flags",
			fn:   "equality",
			want: NewArray([]Value{NewBool(true), NewBool(false), NewBool(false), NewBool(true)}),
		},
		{name: "case_equality", fn: "case_equality", want: NewBool(true)},
		{name: "case_equality_non_string", fn: "case_equality_non_string", want: NewBool(false)},
		{name: "when_number", fn: "when_matching", args: []Value{NewString("123")}, want: sym("number")},
		{name: "when_word", fn: "when_matching", args: []Value{NewString("abc")}, want: sym("word")},
		{name: "when_other", fn: "when_matching", args: []Value{NewString("a1")}, want: sym("other")},
		{name: "inspect_form", fn: "inspect_form", want: NewString("/b+/i")},
		{name: "interpolation_form", fn: "interpolation_form", want: NewString("/b+/i")},
		{name: "membership_by_equality", fn: "membership", want: NewBool(true)},
		{name: "uniq_by_equality", fn: "unique", want: NewInt(2)},
		{
			name: "regexp_new_returns_regex",
			fn:   "regexp_new_kind",
			want: NewArray([]Value{NewString("ab+"), NewString("abbb"), NewBool(true)}),
		},
		{name: "regexp_union_matches", fn: "regexp_union_matches", want: NewBool(true)},
		{name: "string_match_regex", fn: "string_match_regex", want: NewString("b")},
		{name: "string_match_predicate_regex", fn: "string_match_predicate_regex", want: NewBool(true)},
		{
			name: "string_scan_regex",
			fn:   "string_scan_regex",
			want: NewArray([]Value{
				NewArray([]Value{NewString("1")}),
				NewArray([]Value{NewString("2")}),
				NewArray([]Value{NewString("3")}),
			}),
		},
		{name: "string_gsub_regex", fn: "string_gsub_regex", want: NewString("N12 N34")},
		{name: "string_sub_regex_block", fn: "string_sub_regex_block", want: NewString("hellO world")},
		{name: "string_sub_literal_stays_literal", fn: "string_sub_literal_stays_literal", want: NewString("a!c")},
		{name: "task_boundary_passthrough", fn: "task_boundary", args: []Value{NewNil()}, want: NewInt(2)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, tc.args)
			if diff := valuesDiff([]Value{tc.want}, []Value{got}); diff != "" {
				t.Fatalf("%s mismatch (-want +got):\n%s", tc.fn, diff)
			}
		})
	}
}

// TestRegexLiteralErrors pins the diagnostics for regex misuse: unsupported
// operand kinds, invalid patterns, guarded sizes, and non-hashable keys.
func TestRegexLiteralErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		fn     string
		want   string
	}{
		{
			name:   "match_operand_kinds",
			source: "def run\n  1 =~ /a/\nend",
			fn:     "run",
			want:   "=~ expects a string and a regex operand",
		},
		{
			name:   "not_match_operand_kinds",
			source: "def run\n  /a/ !~ /a/\nend",
			fn:     "run",
			want:   "!~ expects a string and a regex operand",
		},
		{
			name:   "invalid_pattern",
			source: "def run\n  \"x\" =~ /a(/\nend",
			fn:     "run",
			want:   "regex literal invalid regex:",
		},
		{
			name:   "regex_keyword_with_regex_pattern",
			source: "def run\n  \"a\".gsub(/a/, \"b\", regex: true)\nend",
			fn:     "run",
			want:   "string.gsub does not take the regex keyword with a regex pattern",
		},
		{
			name:   "unknown_member_suggestion",
			source: "def run\n  /a/.matches?(\"a\")\nend",
			fn:     "run",
			want:   "unknown regex method matches?",
		},
		{
			name:   "regex_not_hashable",
			source: "def run\n  h = {}\n  h[/a/] = 1\nend",
			fn:     "run",
			want:   "unsupported hash key type regex",
		},
		{
			name:   "json_stringify_rejects_regex",
			source: "def run\n  JSON.stringify({ re: /a/ })\nend",
			fn:     "run",
			want:   "JSON.stringify unsupported value type regex",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCallErrorContains(t, script, tc.fn, nil, CallOptions{}, tc.want)
		})
	}
}

// TestRegexLiteralGuards pins the sandbox limits on regex literal matching,
// including the case-equality paths (===, case/when, and Array#grep) that
// reach the matcher without going through =~.
func TestRegexLiteralGuards(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 8 << 20}, `
    def oversized_match(text)
      text =~ /a/
    end

    def oversized_case_eq(text)
      /a/ === text
    end

    def oversized_case_when(text)
      case text
      when /a/
        true
      else
        false
      end
    end

    def oversized_grep(text)
      [text].grep(/a/)
    end
    `)

	huge := strings.Repeat("x", maxRegexInputBytes+1)
	requireCallErrorContains(t, script, "oversized_match", []Value{NewString(huge)}, CallOptions{}, "=~ text exceeds limit")
	requireCallErrorContains(t, script, "oversized_case_eq", []Value{NewString(huge)}, CallOptions{}, "regex match text exceeds limit")
	requireCallErrorContains(t, script, "oversized_case_when", []Value{NewString(huge)}, CallOptions{}, "regex match text exceeds limit")
	requireCallErrorContains(t, script, "oversized_grep", []Value{NewString(huge)}, CallOptions{}, "regex match text exceeds limit")
}
