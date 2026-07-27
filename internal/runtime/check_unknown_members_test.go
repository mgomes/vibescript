package runtime

import (
	"context"
	"strings"
	"testing"
)

// vibes check accepted an unknown method on a receiver whose type it
// statically knows, so a misspelled member -- the most frequent authoring
// mistake there is -- was unassisted at check time and surfaced only at
// runtime.
func TestCheckRejectsUnknownMemberOnKnownReceiver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "string", source: "def f(s: string) -> string\n  s.uppercase\nend", want: "unknown string member uppercase"},
		{name: "array typo", source: "def f(items: array<int>) -> int\n  items.lengt\nend", want: "unknown array member lengt"},
		{name: "int typo", source: "def f(n: int) -> int\n  n.abz\nend", want: "unknown int member abz"},
		{name: "symbol", source: "def f(s: symbol) -> string\n  s.nope\nend", want: "unknown symbol member nope"},
		{name: "literal receiver", source: "def f() -> string\n  \"x\".uppercase\nend", want: "unknown string member uppercase"},
		{name: "array literal receiver", source: "def f() -> int\n  [1].lengt\nend", want: "unknown array member lengt"},
		{name: "method call form", source: "def f(s: string) -> string\n  s.uppercase()\nend", want: "unknown string member uppercase"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCheckWarningContains(t, script, tc.want)
		})
	}
}

// A near miss carries the suggestion the runtime already offered, which is
// most of the value: the author sees the right name rather than only that the
// wrong one is wrong.
func TestUnknownMemberDiagnosticSuggests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source string
		want   string
	}{
		{source: "def f(items: array<int>) -> int\n  items.lengt\nend", want: `did you mean "length"`},
		{source: "def f(n: int) -> int\n  n.abz\nend", want: `did you mean "abs"`},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCheckWarningContains(t, script, tc.want)
		})
	}
}

// The check must stay silent wherever the receiver could dispatch elsewhere.
// A false "unknown method" on working code is far worse than saying nothing,
// so each of these is a soundness boundary rather than a nicety.
func TestKnownMembersAndDynamicReceiversAreNotReported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{name: "real member", source: "def f(s: string) -> string\n  s.upcase\nend"},
		{name: "universal member", source: "def f(s: string) -> bool\n  s.nil?\nend"},
		{name: "universal predicate", source: "def f(s: string) -> bool\n  s.respond_to?(:upcase)\nend"},
		// An unannotated parameter dispatches on whatever arrives.
		{name: "unannotated parameter", source: "def f(x) -> int\n  x.whatever\nend"},
		// A kind whose dispatch is a switch with a hand-maintained parallel
		// list is excluded: the list could drift behind the switch, and a
		// drifted entry would report a working member.
		{name: "duration receiver", source: "def f(d: duration) -> string\n  d.anything_at_all\nend"},
		{name: "time receiver", source: "def f(t: time) -> string\n  t.anything_at_all\nend"},
		{name: "range receiver", source: "def f(r: range) -> int\n  r.anything_at_all\nend"},
		// A member every arm of a union has.
		{name: "union with a shared member", source: "def f(v: string | int) -> string\n  v.to_s\nend"},
		// A nullable receiver dispatches on nil too.
		{name: "nullable receiver", source: "def f(s: string?) -> string\n  s.upcase\nend"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			for _, warning := range script.CheckWarnings() {
				if strings.Contains(warning.Message, "unknown") && strings.Contains(warning.Message, "member") {
					t.Fatalf("%s: unexpected unknown-member diagnostic: %v", tc.name, warning)
				}
			}
		})
	}
}

// A union reports only when the member is unknown for every arm, so a value
// that might be either kind is reported solely when the call fails whichever
// it turns out to be.
func TestUnionReportsOnlyWhenEveryArmLacksTheMember(t *testing.T) {
	t.Parallel()

	script := compileScript(t, "def f(v: string | int) -> string\n  v.definitely_not_a_member\nend")
	requireCheckWarningContains(t, script, "unknown")

	shared := compileScript(t, "def f(v: string | int) -> string\n  v.inspect\nend")
	for _, warning := range shared.CheckWarnings() {
		if strings.Contains(warning.Message, "unknown") && strings.Contains(warning.Message, "member") {
			t.Fatalf("a member both arms have was reported: %v", warning)
		}
	}
}

// The check agrees with the runtime: everything it reports really does fail.
func TestReportedMembersAlsoFailAtRuntime(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`"x".uppercase`, `[1].lengt`, `(1).abz`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			if len(script.CheckWarnings()) == 0 {
				t.Fatalf("%s was not reported by the checker", expr)
			}
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("%s was reported but succeeds at runtime", expr)
			}
		})
	}
}
