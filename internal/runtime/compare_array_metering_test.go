package runtime

import (
	"context"
	"strings"
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

// The ordering members replace a comparison failure with their own "values are
// not comparable" message. Now that comparison charges steps, that replacement
// would relabel a quota exhaustion as an ordinary runtime error -- and an
// ordinary error is rescuable, so a sandbox limit would become catchable.
func TestSortPreservesSandboxErrors(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 400, MemoryQuotaBytes: 64 << 20}, `
    def run(rows)
      begin
        rows.sort.length
      rescue => e
        "rescued"
      end
    end
    `)
	rows := make([]Value, 300)
	for i := range rows {
		inner := make([]Value, 200)
		for j := range inner {
			inner[j] = NewInt(1)
		}
		inner[len(inner)-1] = NewInt(int64(i))
		rows[i] = NewArray(inner)
	}
	_, err := script.Call(context.Background(), "run", []Value{NewArray(rows)}, CallOptions{})
	if err == nil {
		t.Fatalf("the step quota was caught by rescue: a sandbox limit must stay uncatchable")
	}
	if !strings.Contains(err.Error(), "step quota") {
		t.Fatalf("error = %v, want the step-quota error rather than an incomparability message", err)
	}
}

// A genuine incomparability still reports as one.
func TestSortStillReportsIncomparableValues(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      [[1, "a"], [1, 2]].sort
    end
    `)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected incomparable values to be reported")
	}
	if !strings.Contains(err.Error(), "not comparable") {
		t.Fatalf("error = %v, want the incomparability message", err)
	}
}

// The memo is a Go-local map, invisible to the periodic memory check, so
// bounding the walk's CPU with it would otherwise trade unbounded time for
// unbounded host memory: two equal structures with many distinct nested pairs
// retain one entry each until the comparison finishes.
func TestComparisonMemoIsChargedAgainstTheMemoryQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 220 * 1024}, `
    def run(a, b)
      (a <=> b).inspect
    end
    `)
	// Many distinct nested pairs, so the memo grows one entry per pair.
	build := func() Value {
		rows := make([]Value, 4000)
		for i := range rows {
			rows[i] = NewArray([]Value{NewInt(int64(i)), NewInt(int64(i))})
		}
		return NewArray(rows)
	}
	if _, err := script.Call(context.Background(), "run", []Value{build(), build()}, CallOptions{}); err == nil {
		t.Fatalf("expected the memo's growth to be visible to the memory quota")
	}
}

// The memo's reservation must not change the answer for an ordinary
// comparison that fits comfortably.
func TestComparisonStaysCorrectWithTheMemoReservation(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 2 << 20}, `
    def run(a, b)
      (a <=> b).inspect
    end
    `)
	build := func(last int64) Value {
		rows := make([]Value, 200)
		for i := range rows {
			rows[i] = NewArray([]Value{NewInt(int64(i))})
		}
		rows[len(rows)-1] = NewArray([]Value{NewInt(last)})
		return NewArray(rows)
	}
	got, err := script.Call(context.Background(), "run", []Value{build(1), build(2)}, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "-1" {
		t.Fatalf("comparison = %s, want -1", got.String())
	}
}

// A memo entry that does not fit is dropped, and its reservation must be
// rolled back: repeated failed insertions would otherwise accumulate phantom
// scratch and shrink the budget for everything after them.
func TestDroppedMemoEntriesLeaveNoReservation(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 4 << 20}, `
    def run(a, b, filler)
      first = (a <=> b).inspect
      # A second comparison must still have its full budget: the first one's
      # dropped memo entries must not have consumed any.
      second = (a <=> b).inspect
      "#{first}#{second}"
    end
    `)
	build := func() Value {
		rows := make([]Value, 3000)
		for i := range rows {
			rows[i] = NewArray([]Value{NewInt(int64(i)), NewInt(int64(i))})
		}
		return NewArray(rows)
	}
	got, err := script.Call(context.Background(), "run", []Value{build(), build(), NewNil()}, CallOptions{})
	if err != nil {
		t.Fatalf("repeated comparisons exhausted the quota through leaked reservations: %v", err)
	}
	if got.String() != "00" {
		t.Fatalf("comparisons = %s, want 00", got.String())
	}
}
