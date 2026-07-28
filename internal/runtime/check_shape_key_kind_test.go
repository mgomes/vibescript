package runtime

import (
	"strings"
	"testing"
)

// A shape parameter accepts either key kind, so a read of even a required
// field joins nil. That is sound, but the bare "got string | nil" reads as
// though the field were optional -- which is how #1046 concluded that `?` had
// no effect. The diagnostic now names the real cause, and only for the case it
// describes.
func TestRequiredShapeFieldReadExplainsTheUnknownKeyKind(t *testing.T) {
	t.Parallel()

	const hint = "key kind is unknown"

	tests := []struct {
		name     string
		source   string
		wantHint bool
	}{
		{
			name: "required field on a shape parameter",
			source: `def take(name: string) -> string
  name
end
def f(row: { name: string }) -> string
  take(row[:name])
end`,
			wantHint: true,
		},
		{
			name: "optional field says nothing extra",
			source: `def take(name: string) -> string
  name
end
def f(row: { name?: string }) -> string
  take(row[:name])
end`,
			wantHint: false,
		},
		{
			name: "string-key read of a required field",
			source: `def take(name: string) -> string
  name
end
def f(row: { name: string }) -> string
  take(row["name"])
end`,
			wantHint: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			messages := make([]string, 0, 4)
			for _, warning := range script.CheckWarnings() {
				messages = append(messages, warning.Message)
			}
			joined := strings.Join(messages, "\n")
			if joined == "" {
				t.Fatalf("%s produced no diagnostic", tc.name)
			}
			if got := strings.Contains(joined, hint); got != tc.wantHint {
				t.Fatalf("%s: hint present = %v, want %v\n%s", tc.name, got, tc.wantHint, joined)
			}
		})
	}
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

// A later argument that mutates the shape an earlier one read must not lose the
// explanation. This passes with or without the captured-hint change -- none of
// delete, clear, merge!, or a mutating call poisons the fact enough to drop it
// -- so it pins the property rather than covering a fixed defect.
func TestShapeKeyKindHintSurvivesLaterArgumentMutation(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def accept(a: string, b)
  a
end
def f(row: { name: string })
  accept(row[:name], row.delete(:name))
end`)

	messages := make([]string, 0, 4)
	for _, warning := range script.CheckWarnings() {
		messages = append(messages, warning.Message)
	}
	joined := strings.Join(messages, "\n")
	if joined == "" {
		t.Fatalf("no diagnostic for the mutated-shape call")
	}
	if !strings.Contains(joined, "key kind is unknown") {
		t.Fatalf("the explanation was lost when a later argument mutated the shape:\n%s", joined)
	}
}
