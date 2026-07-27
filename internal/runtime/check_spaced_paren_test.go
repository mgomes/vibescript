package runtime

import (
	"strings"
	"testing"
)

func spacedParenScript(t *testing.T, source string) *Script {
	t.Helper()
	engine := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited, RecursionLimit: 10_000})
	script, err := engine.CompileSnippet(source, "__main__")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return script
}

// A space before a parenthesised argument changes how the rest of the
// expression binds, and it binds differently than in Ruby: `f (x).length` is
// `(f(x)).length` here and `f((x).length)` there. Both produce a value and
// neither reported anything, so the two readings were indistinguishable from
// the source.
func TestSpacedParenBeforeMemberAccessIsReported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{name: "call result takes a member", source: "def f(v)\n  v\nend\nx = [1, 2, 3]\ny = f (x).length"},
		// The debugging-shaped case: the one line written to see a value
		// accurately renders it inaccurately.
		{name: "puts with inspect", source: `x = ["a", "b"]` + "\nputs (x).inspect"},
		{name: "inside a function body", source: "def g(v)\n  v\nend\ndef run(x)\n  g (x).length\nend"},
		{name: "chained member", source: "def g(v)\n  v\nend\nx = [1]\ny = g (x).first.to_s"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := spacedParenScript(t, tc.source)
			warnings := script.CheckWarnings()
			found := false
			for _, warning := range warnings {
				if strings.Contains(warning.Message, "space before the parenthesis") {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s: no spaced-paren diagnostic in %v", tc.name, warnings)
			}
		})
	}
}

// The diagnostic must fire only where the space changes the meaning. A call
// whose result is not used as a receiver reads the same either way, and a
// call written without the space is unambiguous.
func TestUnambiguousCallShapesAreNotReported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{name: "no space", source: "def f(v)\n  v\nend\nx = [1]\ny = f(x).length"},
		{name: "spaced call with no member access", source: "def f(v)\n  v\nend\nx = [1]\ny = f (x)"},
		{name: "argument already fully parenthesised", source: "def f(v)\n  v\nend\nx = [1]\ny = f((x).length)"},
		{name: "paren-less call", source: "def f(v)\n  v\nend\nx = [1]\ny = f x"},
		{name: "member access on a plain value", source: "x = [1]\ny = x.length"},
		{name: "member access on a parenthesised expression", source: "x = [1]\ny = (x).length"},
		{name: "method call with no space", source: "x = [1, 2]\ny = x.take(1).length"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := spacedParenScript(t, tc.source)
			for _, warning := range script.CheckWarnings() {
				if strings.Contains(warning.Message, "space before the parenthesis") {
					t.Fatalf("%s: unexpected spaced-paren diagnostic: %v", tc.name, warning)
				}
			}
		})
	}
}

// The message has to name both readings, because which one the language picked
// is exactly what the author cannot see.
func TestSpacedParenDiagnosticNamesBothReadings(t *testing.T) {
	t.Parallel()
	script := spacedParenScript(t, "def f(v)\n  v\nend\nx = [1]\ny = f (x).length")
	var message string
	for _, warning := range script.CheckWarnings() {
		if strings.Contains(warning.Message, "space before the parenthesis") {
			message = warning.Message
			break
		}
	}
	if message == "" {
		t.Fatalf("no spaced-paren diagnostic")
	}
	for _, want := range []string{"f(...)", "f((...).member)", "Ruby", "remove the space"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message %q missing %q", message, want)
		}
	}
}

// The binding itself is unchanged: this reports the ambiguity rather than
// resolving it, so programs that work today keep their current meaning.
func TestSpacedParenBindingIsUnchanged(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def f(v)
      v
    end
    def run()
      x = [1, 2, 3]
      f (x).length
    end
    `)
	got := callFunc(t, script, "run", nil)
	if got.String() != "3" {
		t.Fatalf("f (x).length = %s, want 3 (the existing binding)", got.String())
	}
}
