package runtime

import (
	"testing"
)

func TestRequiredShapeFieldReadsExactlyUnderUnifiedKeyspace(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `def take(name: string) -> string
  name
end
def f(row: { name: string }) -> string
  take(row[:name])
end`))

	requireNoCheckWarnings(t, compileScript(t, `def take(name: string) -> string
  name
end
def f(row: { name: string }) -> string
  take(row["name"])
end`))

	requireCheckWarningContains(t, compileScript(t, `def take(name: string) -> string
  name
end
def f(row: { name?: string }) -> string
  take(row[:name])
end`), "expected string, got string | nil")

	requireCheckWarningContains(t, compileScript(t, `def take(name: string) -> string
  name
end
def f(row: { name: string | nil }) -> string
  take(row[:name])
end`), "expected string, got string | nil")
}

// Where the key kind is known -- a hash literal -- a required field already
// reads exactly, so there is nothing to explain and no diagnostic at all.
// This is the case that shows `?` is not inert.
func TestKnownKeyKindReadsRequiredFieldsExactly(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `def take(name: string) -> string
  name
end
def f() -> string
  row = {name: "a"}
  take(row[:name])
end`))

	requireCheckWarningContains(t, compileScript(t, `def take(name: string) -> string
  name
end
def f(row: { name?: string }) -> string
  take(row[:name])
end`), "expected string, got string | nil")
}

func TestStaticHashProjectionUsesUnifiedKeyspace(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `def f() -> int
  {name: "old", "name": 1}[:name]
end`))

	requireNoCheckWarnings(t, compileScript(t, `def f() -> string
  {name: "Ada"}["name"]
end`))

	script := compileScriptDefault(t, `def run()
  {name: "old", "name": 1}[:name]
end`)
	if got := callFunc(t, script, "run", nil); got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("{name: \"old\", \"name\": 1}[:name] = %v, want 1", got)
	}
}

func TestRequiredShapeFieldReadStaysExactWhenALaterArgumentMutates(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `def accept(a: string, b)
  a
end
def f(row: { name: string })
  accept(row[:name], row.delete(:name))
end`))
}

// An argument with no hint at evaluation time must not pick one up later. The
// captured map stores an entry even when the hint is empty, because an absent
// entry falls back to re-inference and a later argument's state could then
// supply an explanation for this one -- a wrong reason, which is worse than
// none.
func TestAbsentHintIsCapturedRatherThanReinferred(t *testing.T) {
	t.Parallel()

	read := &IndexExpr{
		Object:  &Identifier{Name: "row"},
		Indices: []Expression{&SymbolLiteral{Name: "name"}},
	}

	c := &scriptChecker{}
	// An explicitly captured empty entry must win over any later derivation.
	// Without the entry the lookup falls through to re-inference, which is the
	// path that can borrow a later argument's state.
	c.callArgumentHints = map[Expression]string{read: ""}
	if got := c.capturedShapeKeyKindHint(read); got != "" {
		t.Fatalf("captured-empty hint = %q, want it to stay empty", got)
	}

	// A captured hint is used as captured.
	const captured = "; name is required, but this shape's key kind is unknown, so the read may still miss"
	c.callArgumentHints = map[Expression]string{read: captured}
	if got := c.capturedShapeKeyKindHint(read); got != captured {
		t.Fatalf("captured hint = %q, want %q", got, captured)
	}
}
