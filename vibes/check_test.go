package vibes_test

import (
	"testing"

	"github.com/mgomes/vibescript/vibes"
)

// The checker's diagnostic type must be nameable from external embedder code
// using only the public vibes package: APIs that store, filter, or forward
// warnings need the name, not just the returned values.
func TestCheckWarningPublicType(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile(`
def takes_int(value: int)
  value
end

def run()
  takes_int("nope")
end
`)
	if err != nil {
		t.Fatal(err)
	}

	var warnings []vibes.CheckWarning
	warnings = script.CheckWarnings()
	if len(warnings) != 1 {
		t.Fatalf("CheckWarnings() = %v, want one warning", warnings)
	}
	w := warnings[0]
	if w.Function != "run" {
		t.Fatalf("warning Function = %q, want run", w.Function)
	}
	var pos vibes.Position
	pos = w.Pos
	if pos.Line == 0 {
		t.Fatalf("warning Pos = %+v, want a source position", pos)
	}
	if w.Message == "" || w.Source != "" {
		t.Fatalf("warning = %+v, want message and empty source", w)
	}
}
