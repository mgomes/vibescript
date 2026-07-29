package runtime

import (
	"context"
	"fmt"
	"testing"
)

// loopMemoSource is a read-only loop over a host-supplied array. It allocates
// nothing per iteration, so every byte the estimator reports is already
// reachable before the loop starts and a correct estimator walks it once.
const loopMemoSource = "def run(a, n)\n  t = 0\n  j = 0\n  while j < n\n    t = t + a[j]\n    j = j + 1\n  end\n  t\nend"

func loopMemoArray(n int) Value {
	elems := make([]Value, n)
	for i := range elems {
		elems[i] = NewInt(int64(i))
	}
	return NewArray(elems)
}

// estimatorVisitsFor counts the graph nodes the estimator visits while running
// src under an enforced memory quota. Node counts are exact and machine
// independent, unlike the wall-clock this regression would otherwise be pinned
// with.
func estimatorVisitsFor(t *testing.T, src string, arg Value, n int) uint64 {
	t.Helper()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 64 << 20, StepQuota: Unlimited}, src)
	estimatorVisits.Store(0)
	estimatorVisitCounting.Store(true)
	defer estimatorVisitCounting.Store(false)
	if _, err := script.Call(context.Background(), "run", []Value{arg, NewInt(int64(n))}, CallOptions{}); err != nil {
		t.Fatalf("run(%d): %v", n, err)
	}
	return estimatorVisits.Load()
}

// A statement list evaluates in the scope it is handed rather than a fresh one,
// so a loop body re-pushes the enclosing scope every iteration. Treating that
// duplicate slot as a topology change invalidated the base-walk memo twice per
// iteration, and each miss re-walks the whole reachable graph -- so a loop that
// only reads made the estimator do work quadratic in its own iteration count
// (#1130). Doubling the iterations must now roughly double the visits, not
// quadruple them.
func TestLoopBodyScopeReuseKeepsBaseWalkMemo(t *testing.T) {
	const small, large = 1000, 2000

	atSmall := estimatorVisitsFor(t, loopMemoSource, loopMemoArray(small), small)
	atLarge := estimatorVisitsFor(t, loopMemoSource, loopMemoArray(large), large)

	// Linear growth is 2x for a doubling. Allow generous headroom so the bound
	// tracks the complexity class rather than the exact node count, while
	// staying far below the 4x a quadratic walk produces.
	if atLarge > atSmall*3 {
		t.Errorf("estimator visited %d nodes for %d iterations and %d for %d; "+
			"doubling the iterations should roughly double the walk, so the loop "+
			"body's scope re-push is still invalidating the base-walk memo",
			atSmall, small, atLarge, large)
	}
}

// minMemoryQuotaToComplete binary-searches the smallest memory quota at which
// src completes. It is the observable edge of the estimator's total: the exact
// quota where one more byte of estimate would have failed the run.
func minMemoryQuotaToComplete(t *testing.T, src string, arg Value, n, hi int) int {
	t.Helper()

	lo := 1
	for lo < hi {
		mid := (lo + hi) / 2
		script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: mid, StepQuota: Unlimited}, src)
		if _, err := script.Call(context.Background(), "run", []Value{arg, NewInt(int64(n))}, CallOptions{}); err != nil {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Skipping the topology bump for a duplicate scope slot is sound only if the
// estimate is unchanged by it, and the estimator's own oracle cannot vouch for
// that here: it verifies the reference walk against the memo on a miss, while
// this change exists to turn misses into hits. So compare the memoized total
// against the uncached reference walk at the point where the difference is
// observable -- the exact quota at which a script stops fitting.
//
// This covers the hit path across several loop shapes, which nothing else does,
// but it is not the guard against skipping the bump too widely: writes bump the
// epoch, so a script cannot easily reach a distinct scope push that changes the
// byte total without one, and widening the skip to every push leaves these
// workloads agreeing. TestBaseWalkMemoIsUsedAndInvalidated is that guard -- it
// poisons the memo to detect a reuse and requires a distinct env's push and pop
// to discard it.
//
// Not parallel: baseWalkCacheDisabled is process-wide.
func TestDuplicateScopeSlotEstimateMatchesUncachedWalk(t *testing.T) {
	sources := []struct {
		name string
		src  string
		arg  func(int) Value
	}{
		{
			name: "read-only loop",
			src:  loopMemoSource,
			arg:  loopMemoArray,
		},
		{
			// Grows a container the enclosing scope owns, so the loop body's
			// writes land in a scope that is live across every iteration.
			name: "accumulating loop",
			src:  "def run(a, n)\n  out = []\n  j = 0\n  while j < n\n    out = out.push(a[j])\n    j = j + 1\n  end\n  out.length\nend",
			arg:  loopMemoArray,
		},
		{
			// Rebinds a growing string in the enclosing scope: the byte total
			// genuinely changes every iteration, so the memo must keep being
			// invalidated by the epoch even though the slot is a duplicate.
			name: "growing string loop",
			src:  "def run(a, n)\n  s = \"\"\n  j = 0\n  while j < n\n    s = s + \"xxxxxxxx\"\n    j = j + 1\n  end\n  s.length\nend",
			arg:  func(int) Value { return NewNil() },
		},
		{
			// A nested loop re-pushes the same scope at two depths.
			name: "nested loop",
			src:  "def run(a, n)\n  t = 0\n  j = 0\n  while j < n\n    k = 0\n    while k < 4\n      t = t + k\n      k = k + 1\n    end\n    j = j + 1\n  end\n  t\nend",
			arg:  loopMemoArray,
		},
	}

	const n, hi = 200, 8 << 20
	for _, tc := range sources {
		t.Run(tc.name, func(t *testing.T) {
			arg := tc.arg(n)

			baseWalkCacheDisabled.Store(true)
			uncached := minMemoryQuotaToComplete(t, tc.src, arg, n, hi)
			baseWalkCacheDisabled.Store(false)

			memoized := minMemoryQuotaToComplete(t, tc.src, arg, n, hi)

			if memoized != uncached {
				t.Errorf("smallest quota that fits is %d with the base-walk memo and "+
					"%d with the reference walk; the memo must not change the estimate, "+
					"and a lower memoized figure means a suppressed topology bump left "+
					"it stale", memoized, uncached)
			}
		})
	}
}

// A loop whose body genuinely grows reachable memory must still be stopped by
// the quota. The duplicate-slot skip suppresses only the topology bump, never
// the mutation epoch, so growth stays visible.
func TestLoopUnderQuotaStillFailsWhenItGrows(t *testing.T) {
	t.Parallel()

	src := "def run(a, n)\n  out = []\n  j = 0\n  while j < n\n    out = out.push(\"" + fmt.Sprintf("%0*d", 512, 0) + "\")\n    j = j + 1\n  end\n  out.length\nend"
	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 256 << 10, StepQuota: Unlimited}, src)
	if _, err := script.Call(context.Background(), "run", []Value{NewNil(), NewInt(100000)}, CallOptions{}); err == nil {
		t.Fatal("a loop appending 100k half-kilobyte strings completed under a 256 KiB memory quota")
	}
}
