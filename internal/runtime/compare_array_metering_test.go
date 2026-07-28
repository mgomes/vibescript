package runtime

import (
	"context"
	"testing"
	"time"
)

const sharedDAGSource = `
def build(d)
  cur = [1]
  i = 0
  while i < d
    cur = [cur, cur]
    i = i + 1
  end
  cur
end
`

// Deleting each pair from the on-stack set as soon as it returned made two
// sibling branches re-walk the same subtree, so comparing shared DAGs did 2^d
// work -- measured at 152ms, 551ms, and 2.1s for depths 20, 22, and 24, with
// the step quota never firing because nothing in the walk charged a step. A
// script could monopolize the runtime from inside <=> whatever its limits.
func TestSharedDAGComparisonIsNotExponential(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 5_000_000, MemoryQuotaBytes: 64 << 20}, sharedDAGSource+`
    def run()
      a = build(24)
      b = build(24)
      (a <=> b).inspect
    end
    `)
	done := make(chan struct{})
	var got Value
	var err error
	go func() {
		defer close(done)
		got, err = script.Call(context.Background(), "run", nil, CallOptions{})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("comparing two 24-deep shared DAGs did not finish: the walk is exponential again")
	}
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "0" {
		t.Fatalf("shared DAGs compared to %s, want 0", got.String())
	}
}

// Every compared element charges a step, so a long comparison is bounded by
// the step quota and observes cancellation.
func TestArrayComparisonChargesSteps(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 300, MemoryQuotaBytes: 64 << 20}, `
    def run(a, b)
      (a <=> b).inspect
    end
    `)
	wide := make([]Value, 20000)
	for i := range wide {
		wide[i] = NewInt(1)
	}
	other := append([]Value{}, wide...)
	if _, err := script.Call(context.Background(), "run", []Value{NewArray(wide), NewArray(other)}, CallOptions{}); err == nil {
		t.Fatalf("expected the step quota to stop a 20000-element comparison")
	}
}

// sort routes through the same comparator, so it is metered too.
func TestArraySortChargesStepsForElementComparisons(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 400, MemoryQuotaBytes: 64 << 20}, `
    def run(rows)
      rows.sort.length
    end
    `)
	// The rows must differ only at the end, so each comparison walks the whole
	// width rather than short-circuiting on equality or on the first element.
	rows := make([]Value, 300)
	for i := range rows {
		inner := make([]Value, 200)
		for j := range inner {
			inner[j] = NewInt(1)
		}
		inner[len(inner)-1] = NewInt(int64(i))
		rows[i] = NewArray(inner)
	}
	if _, err := script.Call(context.Background(), "run", []Value{NewArray(rows)}, CallOptions{}); err == nil {
		t.Fatalf("expected the step quota to stop sorting wide arrays")
	}
}

// Equal elements form an equal prefix even when their kind has no ordering,
// so two equal hashes no longer make the whole comparison incomparable.
func TestEqualNonOrderableElementsCompareEqual(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{`([{a: 1}] <=> [{a: 1}]).inspect`, "0"},
		{`([{a: 1}, 1] <=> [{a: 1}, 2]).inspect`, "-1"},
		{`([{a: 1}] <=> [{a: 2}]).inspect`, "nil"},
		{`([nil] <=> [nil]).inspect`, "0"},
		{`([[1]] <=> [[1]]).inspect`, "0"},
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

// Memoizing completed pairs must not change results: a pair compared under an
// open cycle uses the equal-on-cycle shortcut and is deliberately not cached.
func TestCyclicComparisonStillTerminatesAndAgrees(t *testing.T) {
	t.Parallel()

	tests := []struct {
		body string
		want string
	}{
		{"a = [1]\n  a.push(a)\n  (a <=> a).inspect", "0"},
		{"a = [1]\n  a.push(a)\n  b = [1]\n  b.push(b)\n  (a <=> b).inspect", "0"},
		{"inner = [1, 2]\n  (([inner, inner, 1]) <=> ([inner, inner, 2])).inspect", "-1"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%v", err)
			}
			if got.String() != tc.want {
				t.Fatalf("= %s, want %s", got.String(), tc.want)
			}
		})
	}
}
