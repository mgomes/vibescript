package value_test

import (
	"regexp"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

func TestRegexValueBasics(t *testing.T) {
	t.Parallel()

	if got := value.KindRegex.String(); got != "regex" {
		t.Fatalf("KindRegex.String() = %q, want %q", got, "regex")
	}

	compiled := regexp.MustCompile("(?i)a+")
	re := value.NewRegex(value.Regex{Source: "a+", Flags: "i", Compiled: compiled})
	if got := re.Kind(); got != value.KindRegex {
		t.Fatalf("Kind() = %v, want KindRegex", got)
	}
	if got := re.String(); got != "/a+/i" {
		t.Fatalf("String() = %q, want %q", got, "/a+/i")
	}
	if got := re.Inspect(); got != "/a+/i" {
		t.Fatalf("Inspect() = %q, want %q", got, "/a+/i")
	}
	if !re.Truthy() {
		t.Fatalf("Truthy() = false, want true")
	}
	if got := re.Regex().Source; got != "a+" {
		t.Fatalf("Regex().Source = %q, want %q", got, "a+")
	}

	same := value.NewRegex(value.Regex{Source: "a+", Flags: "i", Compiled: regexp.MustCompile("(?i)a+")})
	if !re.Equal(same) {
		t.Fatalf("regexes with same source and flags should be equal")
	}
	differentFlags := value.NewRegex(value.Regex{Source: "a+", Flags: "", Compiled: regexp.MustCompile("a+")})
	if re.Equal(differentFlags) {
		t.Fatalf("regexes with different flags should not be equal")
	}
	differentSource := value.NewRegex(value.Regex{Source: "b+", Flags: "i", Compiled: regexp.MustCompile("(?i)b+")})
	if re.Equal(differentSource) {
		t.Fatalf("regexes with different sources should not be equal")
	}

	if _, err := value.HashKeyString(re); err == nil {
		t.Fatalf("HashKeyString(regex) error = nil, want unsupported hash key")
	}
}

// TestRegexStringEscapesDelimiters pins that rendering escapes unescaped
// delimiter slashes and control characters so a regex built from a string
// (Regexp.new/union) produces a valid, round-trippable literal instead of /a/b/
// or one with an embedded newline the lexer rejects.
func TestRegexStringEscapesDelimiters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		flags  string
		want   string
	}{
		{name: "raw slash from string source", source: "a/b", want: `/a\/b/`},
		{name: "already escaped slash left as-is", source: `a\/b`, want: `/a\/b/`},
		{name: "escaped backslash before slash", source: `a\\/b`, want: `/a\\\/b/`},
		{name: "leading slash", source: "/x", want: `/\/x/`},
		{name: "no slash unchanged", source: "a+", flags: "i", want: "/a+/i"},
		{name: "newline", source: "a\nb", want: `/a\nb/`},
		{name: "tab and slash", source: "a\t/", want: `/a\t\//`},
		{name: "carriage return", source: "\r", want: `/\r/`},
		{name: "null byte", source: "\x00", want: `/\x{0}/`},
		{name: "unit separator", source: "\x1f", want: `/\x{1f}/`},
		{name: "delete", source: "\x7f", want: `/\x{7f}/`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			re := value.Regex{Source: tc.source, Flags: tc.flags}
			if got := re.String(); got != tc.want {
				t.Fatalf("Regex{Source:%q, Flags:%q}.String() = %q, want %q", tc.source, tc.flags, got, tc.want)
			}
		})
	}
}
