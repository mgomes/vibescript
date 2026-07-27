package runtime

import (
	"context"
	"strings"
	"testing"
)

// to_s and inspect were missing on most value kinds even though every kind
// already rendered correctly through interpolation and inside a container's
// inspect. The rendering existed; only the direct method did not, and which
// kinds had which corresponded to nothing: duration, money, and time had to_s
// but not inspect, array and hash had inspect but not to_s, range had neither.
func TestToStringAndInspectCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "array to_s", expr: `[1, 2].to_s`, want: "[1, 2]"},
		{name: "array string alias", expr: `[1, 2].string`, want: "[1, 2]"},
		{name: "range to_s", expr: `(1..3).to_s`, want: "1..3"},
		{name: "range inspect", expr: `(1..3).inspect`, want: "1..3"},
		{name: "duration inspect", expr: `300.seconds.inspect`, want: "300s"},
		{name: "money inspect", expr: `money("1.00 USD").inspect`, want: "1.00 USD"},
		{name: "time inspect", expr: `Time.parse("2026-01-01T00:00:00Z").inspect`, want: "2026-01-01T00:00:00Z"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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

// The point of the conversion is that it agrees with interpolation, which is
// the rendering that already worked. docs/language_reference.md describes
// interpolation as using "the same string form used by to_s", so these have to
// match rather than merely both exist.
func TestToStringAgreesWithInterpolation(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		`[1, "a"]`,
		`(1..3)`,
		`300.seconds`,
		`money("1.00 USD")`,
		`Time.parse("2026-01-01T00:00:00Z")`,
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  v = "+expr+"\n  (v.to_s == \"#{v}\").to_s\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", expr, err)
			}
			if got.String() != "true" {
				t.Fatalf("%s: to_s disagrees with interpolation", expr)
			}
		})
	}
}

// to_s and inspect are different renderings and must stay different where they
// were: inspect quotes a string element, to_s does not.
func TestAggregateToStringDiffersFromInspect(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      v = [1, "a"]
      "#{v.to_s}|#{v.inspect}"
    end
    `)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != `[1, a]|[1, "a"]` {
		t.Fatalf("to_s|inspect = %q, want `[1, a]|[1, \"a\"]`", got.String())
	}
}

// An aggregate's rendering can be arbitrarily large, which is why to_s was
// withheld in the first place. It now charges the memory quota exactly as
// inspect does, so the reason no longer applies -- but only because the charge
// is really there.
func TestAggregateToStringRespectsMemoryQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 128 * 1024}, `
    def run(rows)
      rows.to_s
    end
    `)
	rows := make([]Value, 20000)
	for i := range rows {
		rows[i] = NewString(strings.Repeat("x", 64))
	}
	if _, err := script.Call(context.Background(), "run", []Value{NewArray(rows)}, CallOptions{}); err == nil {
		t.Fatalf("expected the memory quota to stop rendering a large array with to_s")
	}
}

// The projection walks the value and charges a step per node, so a deeply
// shared graph cannot burn unbounded CPU before the memory check runs.
func TestAggregateToStringChargesSteps(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 200, MemoryQuotaBytes: 32 << 20}, `
    def run(rows)
      rows.to_s
    end
    `)
	rows := make([]Value, 5000)
	for i := range rows {
		rows[i] = NewInt(int64(i))
	}
	if _, err := script.Call(context.Background(), "run", []Value{NewArray(rows)}, CallOptions{}); err == nil {
		t.Fatalf("expected the step quota to stop projecting a large array")
	}
}

// These take no arguments, like every other conversion.
func TestAggregateToStringRejectsArguments(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`[1].to_s(1)`, `(1..3).to_s(1)`, `(1..3).inspect(1)`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("%s was accepted, want it rejected", expr)
			}
		})
	}
}
