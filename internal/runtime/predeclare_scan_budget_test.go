package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// deadBranchScript builds a loop whose body holds a branch that never runs.
// The branch is what the predeclaration scan walks after the enclosing if
// finishes, so its size is host work the loop never executes.
func deadBranchScript(iterations, branchStatements int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "def run()\n  i = 0\n  while i < %d\n    if false\n", iterations)
	for n := range branchStatements {
		fmt.Fprintf(&b, "      dead%d = %d\n", n, n)
	}
	b.WriteString("    end\n    i = i + 1\n  end\n  i\nend")
	return b.String()
}

// minStepsForScript returns the smallest step quota that lets run() finish.
func minStepsForScript(t *testing.T, src string) int {
	t.Helper()

	lo, hi := 1, 4_000_000
	for lo < hi {
		mid := (lo + hi) / 2
		script := compileScriptWithConfig(t, Config{StepQuota: mid, MemoryQuotaBytes: Unlimited}, src)
		if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// TestPredeclareScanChargesTheStatementsItWalks pins that the local
// predeclaration scan costs steps in proportion to the statements it walks.
//
// The scan is Go-side work the per-statement charge never saw, and it is not
// proportional to what ran: a compound statement rescans its whole subtree
// after each nested statement completes, and a branch that was not taken is
// rescanned every time its enclosing statement finishes. A loop over a large
// dead branch therefore bought unbounded host CPU for one step per iteration,
// and nested conditionals cost O(n^2) against an O(n) quota (#27).
func TestPredeclareScanChargesTheStatementsItWalks(t *testing.T) {
	t.Parallel()

	const iterations = 2000
	const narrowBranch = 16
	const wideBranch = 512

	narrow := minStepsForScript(t, deadBranchScript(iterations, narrowBranch))
	wide := minStepsForScript(t, deadBranchScript(iterations, wideBranch))

	// Every iteration walks the whole dead branch, so the wider one must cost
	// the difference at the amortized rate. Unwalked, both scripts execute the
	// identical handful of statements per iteration and cost the same.
	want := narrow + iterations*(wideBranch-narrowBranch)/predeclareScanStatementsPerStep
	if wide < want {
		t.Fatalf("a %d statement dead branch cost %d steps and a %d statement one %d; want at least "+
			"%d, one step per %d statements walked", wideBranch, wide, narrowBranch, narrow, want,
			predeclareScanStatementsPerStep)
	}
}

// TestPredeclareScanLeavesOrdinaryBodiesFree pins that the charge is amortized
// rather than per statement: a body shorter than the amortization window costs
// nothing, so ordinary code keeps the step count it has today.
func TestPredeclareScanLeavesOrdinaryBodiesFree(t *testing.T) {
	t.Parallel()

	const iterations = 500
	small := minStepsForScript(t, deadBranchScript(iterations, 8))
	larger := minStepsForScript(t, deadBranchScript(iterations, predeclareScanStatementsPerStep-4))
	if small != larger {
		t.Fatalf("bodies of 8 and %d statements cost %d and %d steps; neither reaches the %d statement "+
			"window, so both must stay free", predeclareScanStatementsPerStep-4, small, larger,
			predeclareScanStatementsPerStep)
	}
}
