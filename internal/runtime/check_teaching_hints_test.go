package runtime

import (
	"context"
	"strings"
	"testing"
)

func teachingScript(t *testing.T, source string) *Script {
	t.Helper()
	engine := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited, RecursionLimit: 10_000})
	script, err := engine.CompileSnippet(source, "__main__")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return script
}

// Defining a constant at the top of a file and reading it from a function is
// one of the most natural program shapes there is, and "undefined variable
// LIMIT" gave no route forward: didYouMean cannot fire because the name really
// is not in scope, so nothing hinted that the binding exists one scope away.
func TestUndefinedVariableNamesTopLevelBinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{name: "constant", source: "LIMIT = 10\n\ndef f(n)\n  n < LIMIT\nend"},
		{name: "lowercase local", source: "limit = 10\n\ndef f(n)\n  n < limit\nend"},
		{name: "table", source: "RATES = {a: 1}\n\ndef f()\n  RATES\nend"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := teachingScript(t, tc.source)
			var message string
			for _, warning := range script.CheckWarnings() {
				if strings.Contains(warning.Message, "undefined variable") {
					message = warning.Message
					break
				}
			}
			if message == "" {
				t.Fatalf("%s: no undefined-variable diagnostic", tc.name)
			}
			if !strings.Contains(message, "do not capture top-level bindings") {
				t.Fatalf("%s: message = %q, want the top-level binding hint", tc.name, message)
			}
		})
	}
}

// A name that is genuinely nowhere must not get the hint, or it points at a
// binding that does not exist.
func TestUndefinedVariableWithoutTopLevelBindingHasNoHint(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		"def f(n)\n  n < NOPE\nend",
		"OTHER = 1\n\ndef f(n)\n  n < NOPE\nend",
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			script := teachingScript(t, source)
			for _, warning := range script.CheckWarnings() {
				if strings.Contains(warning.Message, "do not capture top-level bindings") {
					t.Fatalf("unexpected top-level hint: %v", warning)
				}
			}
		})
	}
}

// attr_accessor and friends are the Ruby spelling of members this language
// provides under other names, and the error named the unknown thing without
// naming the replacement.
func TestClassMacroErrorNamesTheAlternative(t *testing.T) {
	t.Parallel()

	tests := []struct {
		macro string
		want  string
	}{
		{macro: "attr_accessor", want: "property x"},
		{macro: "attr_reader", want: "getter x"},
		{macro: "attr_writer", want: "setter x"},
	}

	for _, tc := range tests {
		t.Run(tc.macro, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "class A\n  "+tc.macro+" :x\nend\ndef run()\n  1\nend")
			_, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err == nil {
				t.Fatalf("%s was accepted, want it reported", tc.macro)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s error = %v, want it to name %q", tc.macro, err, tc.want)
			}
			// The argument shape differs too, so an author who learns only the
			// new name hits a second error.
			if !strings.Contains(err.Error(), "not a symbol") {
				t.Fatalf("%s error = %v, want it to mention the bare name", tc.macro, err)
			}
		})
	}
}

// An unrelated unknown class member keeps the did-you-mean suggestion.
func TestUnrelatedClassMemberKeepsSuggestion(t *testing.T) {
	t.Parallel()
	script := compileScript(t, "class A\n  def self.helper()\n    1\n  end\nend\ndef run()\n  A.helpr\nend")
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected an unknown class member to be reported")
	}
	if !strings.Contains(err.Error(), "helper") {
		t.Fatalf("error = %v, want the did-you-mean suggestion", err)
	}
}
