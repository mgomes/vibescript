package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// hashLiteralLoopSource loops n times over a literal of the given width, keeping
// the host-supplied array a reachable throughout. Duplicate keys drive the
// accumulator into replacement accounting, which is where the snapshot mode
// re-measured every entry against a fresh reference walk.
func hashLiteralLoopSource(pairs int, duplicateKeys bool) string {
	parts := make([]string, 0, pairs)
	for i := range pairs {
		if duplicateKeys {
			parts = append(parts, fmt.Sprintf("k: %d", i))
		} else {
			parts = append(parts, fmt.Sprintf("k%d: %d", i, i))
		}
	}
	return "def run(a, n)\n  t = 0\n  j = 0\n  while j < n\n    t = ({" +
		strings.Join(parts, ", ") + "}).length\n    j = j + 1\n  end\n  t\nend"
}

// minStepQuotaToComplete binary-searches the smallest step quota at which src
// completes, which is the observable total of everything the run charged.
func minStepQuotaToComplete(t *testing.T, src string, arg Value, n, hi int) int {
	t.Helper()

	lo := 1
	for lo < hi {
		mid := (lo + hi) / 2
		script := compileScriptWithConfig(t, Config{StepQuota: mid, MemoryQuotaBytes: 64 << 20}, src)
		if _, err := script.Call(context.Background(), "run", []Value{arg, NewInt(int64(n))}, CallOptions{}); err != nil {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Capping the accumulator's memoized sessions by literal width sent wide
// literals down the snapshot mode, which walks the whole reachable graph at
// construction and, once a duplicate key switches it to replacement accounting,
// once more per pair. The literal's own cost is flat -- small scalar values,
// discarded every iteration -- so a script could hold a large host array
// reachable and turn a few hundred steps into tens of millions of estimator
// visits (#1): 20 iterations of a 256-pair literal over a 10k-element array did
// 51,666,401 visits against 232,941 for a 32-pair one, and the gap grew with the
// array. Widening a literal must not change the complexity of accounting for it.
func TestWideHashLiteralDoesNotRewalkTheReachableGraph(t *testing.T) {
	if estimatorVerify {
		t.Skip("the estimator oracle recomputes a reference walk per check, which is deliberately quadratic")
	}

	const iterations, narrow, wide = 20, 32, 256
	retained := loopMemoArray(10000)

	atNarrow := estimatorVisitsFor(t, hashLiteralLoopSource(narrow, true), retained, iterations)
	atWide := estimatorVisitsFor(t, hashLiteralLoopSource(wide, true), retained, iterations)

	if atWide > atNarrow*2 {
		t.Errorf("estimator visited %d nodes for a %d-pair literal and %d for a %d-pair one over the "+
			"same retained array; the wide literal is re-walking the reachable graph per pair instead "+
			"of resuming the memoized base", atNarrow, narrow, atWide, wide)
	}
}

// Sessions are unavailable while a builtin, a task group, or lazily cloned task
// globals are live, so the snapshot mode's whole-graph walk survives for those
// contexts and has to be paid for rather than avoided. The charge is what let
// the width cap go: it prices the union replay the cap was standing in for, and
// it bounds every other estimator walk a literal can drive. Before it, the same
// literal cost 551 steps whether the retained array held 100 elements or 10,000.
//
// Not parallel: baseWalkCacheDisabled is process-wide. It is the only lever a
// test has to force the snapshot mode, which is otherwise reached through
// builtin depth.
func TestSnapshotHashLiteralChargesTheGraphItWalks(t *testing.T) {
	const iterations, small, large = 20, 100, 10000
	src := hashLiteralLoopSource(8, false)

	baseWalkCacheDisabled.Store(true)
	defer baseWalkCacheDisabled.Store(false)

	atSmall := minStepQuotaToComplete(t, src, loopMemoArray(small), iterations, 1<<20)
	atLarge := minStepQuotaToComplete(t, src, loopMemoArray(large), iterations, 1<<20)

	// Every iteration walks the extra elements once. Half the ideal charge
	// leaves room for the per-charge remainder each stepN rounds away, while
	// staying far above the zero an uncharged walk produces.
	want := atSmall + iterations*(large-small)/estimatorNodesPerStep/2
	if atLarge < want {
		t.Errorf("the same literal cost %d steps over a %d-element retained array and %d over a "+
			"%d-element one; want at least %d, one step per %d nodes the accounting walk visits",
			atSmall, small, atLarge, large, want, estimatorNodesPerStep)
	}
}

// The charge must fall on walks that scale with host data, not on ordinary
// literals: a hash written by hand walks a handful of nodes per entry, which
// rounds to no steps at all. Pin that an everyday literal still evaluates
// correctly inside the default profile's quotas.
func TestOrdinaryHashLiteralStaysWithinDefaultQuotas(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, "def run()\n  h = {name: \"ada\", age: 36, tags: [\"x\", \"y\"], nested: {k: 1}}\n"+
		"  [h[:name], h[:age], h[:tags][1], h[:nested][:k], h.length]\nend")
	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{NewString("ada"), NewInt(36), NewString("y"), NewInt(1), NewInt(4)})
}
