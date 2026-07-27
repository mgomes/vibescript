package runtime

import (
	"context"
	"strings"
	"testing"
)

// String#match carried a NUL-prefixed sentinel key holding the positional
// values, visible in keys, values, to_a, size, each, inspect, and JSON output:
// a key the author never created, whose name is not a valid identifier, and
// which read back as nil through its own name. It could not simply be hidden,
// because the result dispatches as a hash and every surface enumerates the map
// directly.
func TestMatchDataExposesNoInternalKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
	}{
		{name: "keys", expr: `"2026-07".match(/(\d+)-(\d+)/).keys.inspect`},
		{name: "inspect", expr: `"2026-07".match(/(\d+)-(\d+)/).inspect`},
		{name: "to_a", expr: `"2026-07".match(/(\d+)-(\d+)/).to_a.inspect`},
		{name: "values", expr: `"2026-07".match(/(\d+)-(\d+)/).values.inspect`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if strings.Contains(got.String(), "matchData") || strings.Contains(got.String(), "\x00") {
				t.Fatalf("%s still exposes the internal key: %s", tc.name, got.String())
			}
		})
	}
}

// Every key must be a name an author could have written.
func TestMatchDataKeysAreAllPublic(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      "2026-07".match(/(\d+)-(\d+)/).keys.inspect
    end
    `)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	for _, want := range []string{":begin", ":captures", ":end", ":named_captures", ":post_match", ":pre_match", ":to_s"} {
		if !strings.Contains(got.String(), want) {
			t.Fatalf("keys %s missing %s", got.String(), want)
		}
	}
	if strings.Contains(got.String(), `:"`) {
		t.Fatalf("keys %s contains a quoted (non-identifier) name", got.String())
	}
}

// Positional access is rebuilt from the public entries rather than a second
// stored copy, so it has to keep behaving exactly as before.
func TestMatchDataPositionalAccessSurvivesTheRemoval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{`"2026-07".match(/(\d+)-(\d+)/)[0].inspect`, `"2026-07"`},
		{`"2026-07".match(/(\d+)-(\d+)/)[1].inspect`, `"2026"`},
		{`"2026-07".match(/(\d+)-(\d+)/)[2].inspect`, `"07"`},
		{`"2026-07".match(/(\d+)-(\d+)/)[-1].inspect`, `"07"`},
		{`"2026-07".match(/(\d+)-(\d+)/)[-3].inspect`, `"2026-07"`},
		{`"2026-07".match(/(\d+)-(\d+)/)[9].inspect`, "nil"},
		{`"2026-07".match(/(\d+)-(\d+)/)[-9].inspect`, "nil"},
		// A pattern with no capture groups still indexes group 0.
		{`"abc".match(/b/)[0].inspect`, `"b"`},
		{`"abc".match(/b/)[1].inspect`, "nil"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %s, want %s", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// to_s is the whole match, as in Ruby, which is what makes it the right home
// for the value the sentinel used to hold.
func TestMatchDataToStringIsTheWholeMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{`"2026-07".match(/(\d+)-(\d+)/).to_s`, "2026-07"},
		{`"abc".match(/b/).to_s`, "b"},
		// It agrees with group 0, which is the same value by definition.
		{`("2026-07".match(/(\d+)-(\d+)/).to_s == "2026-07".match(/(\d+)-(\d+)/)[0]).to_s`, "true"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// The rest of the shape is untouched.
func TestMatchDataRemainingShapeUnchanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{`"a2026-07b".match(/(\d+)-(\d+)/).captures.inspect`, `["2026", "07"]`},
		{`"a2026-07b".match(/(\d+)-(\d+)/).pre_match.inspect`, `"a"`},
		{`"a2026-07b".match(/(\d+)-(\d+)/).post_match.inspect`, `"b"`},
		{`"a2026-07b".match(/(\d+)-(\d+)/).begin(0).to_s`, "1"},
		{`"x=1".match(/(?<k>\w)=(?<v>\d)/)[:k].inspect`, `"x"`},
		{`"x=1".match(/(?<k>\w)=(?<v>\d)/).named_captures.size.to_s`, "2"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %s, want %s", tc.expr, got.String(), tc.want)
			}
		})
	}
}
