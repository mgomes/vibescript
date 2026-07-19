package vibes_test

import (
	"testing"

	"github.com/mgomes/vibescript/vibes"
)

// The checker's diagnostic type must be nameable from external embedder code
// using only the public vibes package: APIs that store, filter, or forward
// warnings need the name, not just the returned values.
// collectWarnings names the public diagnostic types in an embedder-style API
// signature; compiling it is the point of the test.
func collectWarnings(warnings []vibes.CheckWarning) map[vibes.Position]vibes.CheckWarning {
	out := make(map[vibes.Position]vibes.CheckWarning, len(warnings))
	for _, w := range warnings {
		out[w.Pos] = w
	}
	return out
}

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

	byPos := collectWarnings(script.CheckWarnings())
	if len(byPos) != 1 {
		t.Fatalf("CheckWarnings() = %v, want one warning", byPos)
	}
	for pos, w := range byPos {
		if w.Function != "run" {
			t.Fatalf("warning Function = %q, want run", w.Function)
		}
		if pos.Line == 0 {
			t.Fatalf("warning Pos = %+v, want a source position", pos)
		}
		if w.Message == "" || w.Source != "" {
			t.Fatalf("warning = %+v, want message and empty source", w)
		}
	}
}
