package runtime

import (
	"context"
	"testing"
)

// filter_map was present and flat_map was not, which made the absence look
// arbitrary rather than deliberate. map { }.flatten covers it, so this is
// about the missing shorthand for a very common shape.
func TestArrayFlatMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "flattens one level", expr: `[[1, 2], [3]].flat_map { |x| x }.inspect`, want: "[1, 2, 3]"},
		{name: "expands each element", expr: `[1, 2].flat_map { |x| [x, x] }.inspect`, want: "[1, 1, 2, 2]"},
		// A non-array result contributes itself.
		{name: "non-array results pass through", expr: `[1, 2].flat_map { |x| x }.inspect`, want: "[1, 2]"},
		// Exactly one level, as in Ruby: a nested array inside the result is
		// left alone.
		{name: "only one level is flattened", expr: `[[[1]]].flat_map { |x| x }.inspect`, want: "[[1]]"},
		{name: "mixed array and scalar results", expr: `[1, [2, 3]].flat_map { |x| x }.inspect`, want: "[1, 2, 3]"},
		{name: "empty receiver", expr: `[].flat_map { |x| x }.inspect`, want: "[]"},
		{name: "empty results drop out", expr: `[1, 2].flat_map { |x| [] }.inspect`, want: "[]"},
		{name: "collect_concat is an alias", expr: `[[1], [2]].collect_concat { |x| x }.inspect`, want: "[1, 2]"},
		{name: "composes with other members", expr: `[[1, 2], [3]].flat_map { |x| x }.sum.to_s`, want: "6"},
		{name: "agrees with map then flatten", expr: `([[1, 2], [3]].flat_map { |x| x } == [[1, 2], [3]].map { |x| x }.flatten(1)).to_s`, want: "true"},
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
				t.Fatalf("%s = %s, want %s", tc.expr, got.String(), tc.want)
			}
		})
	}
}

func TestArrayFlatMapRejectsArguments(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`[1].flat_map(2) { |x| x }`, `[1].flat_map(k: 1) { |x| x }`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("%s was accepted, want it rejected", expr)
			}
		})
	}
}

// One block call can contribute arbitrarily many elements, so the accumulating
// result is charged per appended element rather than per call.
func TestArrayFlatMapChargesAccumulatedResult(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 256 * 1024}, `
    def run(rows)
      rows.flat_map { |row| ["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"] * 200 }
    end
    `)
	rows := make([]Value, 200)
	for i := range rows {
		rows[i] = NewInt(int64(i))
	}
	if _, err := script.Call(context.Background(), "run", []Value{NewArray(rows)}, CallOptions{}); err == nil {
		t.Fatalf("expected the memory quota to stop an accumulating flat_map result")
	}
}

// Steps are charged per contributed element, not only per yield, so a block
// that returns a large array cannot traverse it unbounded.
func TestArrayFlatMapChargesStepsPerElement(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 300, MemoryQuotaBytes: 32 << 20}, `
    def run(wide)
      [1, 2, 3].flat_map { |x| wide }
    end
    `)
	wide := make([]Value, 5000)
	for i := range wide {
		wide[i] = NewInt(int64(i))
	}
	if _, err := script.Call(context.Background(), "run", []Value{NewArray(wide)}, CallOptions{}); err == nil {
		t.Fatalf("expected the step quota to stop flattening 15000 elements")
	}
}
