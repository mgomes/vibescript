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

	if _, err := value.NewHashLookupKey(re); err == nil {
		t.Fatalf("NewHashLookupKey(regex) error = nil, want unsupported hash key")
	}
}
