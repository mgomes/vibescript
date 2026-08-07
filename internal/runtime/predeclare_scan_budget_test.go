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
// predeclaration scan costs steps in proportion to the nodes it walks.
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
	// the difference at the amortized rate. The bound counts one node per
	// statement and so understates the real cost, which also walks each
	// statement's target; that keeps it a floor rather than an exact figure.
	// Unwalked, both scripts execute the identical handful of statements per
	// iteration and cost the same.
	want := narrow + iterations*(wideBranch-narrowBranch)/predeclareScanNodesPerStep
	if wide < want {
		t.Fatalf("a %d statement dead branch cost %d steps and a %d statement one %d; want at least "+
			"%d, one step per %d nodes walked", wideBranch, wide, narrowBranch, narrow, want,
			predeclareScanNodesPerStep)
	}
}

// TestPredeclareScanChargesWideDestructureTargets pins that the charge follows
// the names a scan walks, not just the statements holding them.
//
// One destructuring assignment is a single statement but arbitrarily many
// targets, and collecting them is the same per-name work. Counting statements
// alone left a branch of a few very wide assignments under the amortization
// window and therefore free, so a loop could rescan it for nothing.
func TestPredeclareScanChargesWideDestructureTargets(t *testing.T) {
	t.Parallel()

	const iterations = 2000
	const narrowTargets = 4
	const wideTargets = 512

	narrow := minStepsForScript(t, deadDestructureScript(iterations, narrowTargets))
	wide := minStepsForScript(t, deadDestructureScript(iterations, wideTargets))

	want := narrow + iterations*(wideTargets-narrowTargets)/predeclareScanNodesPerStep
	if wide < want {
		t.Fatalf("a %d target destructure cost %d steps and a %d target one %d; want at least %d, "+
			"one step per %d names walked", wideTargets, wide, narrowTargets, narrow, want,
			predeclareScanNodesPerStep)
	}
}

// deadDestructureScript builds a loop whose dead branch holds a single
// destructuring assignment of the given width.
func deadDestructureScript(iterations, targets int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "def run()\n  i = 0\n  while i < %d\n    if false\n      ", iterations)
	for n := range targets {
		if n > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "dead%d", n)
	}
	b.WriteString(" = [1, 2]\n    end\n    i = i + 1\n  end\n  i\nend")
	return b.String()
}

// TestPredeclareScanLeavesOrdinaryBodiesFree pins that the charge is amortized
// rather than per node: a body that does not fill the amortization window costs
// nothing, so ordinary code keeps the step count it has today.
//
// A plain assignment is two nodes, the statement and its target, so both bodies
// here stay well inside the window.
func TestPredeclareScanLeavesOrdinaryBodiesFree(t *testing.T) {
	t.Parallel()

	const iterations = 500
	const wider = predeclareScanNodesPerStep/2 - 4
	small := minStepsForScript(t, deadBranchScript(iterations, 4))
	larger := minStepsForScript(t, deadBranchScript(iterations, wider))
	if small != larger {
		t.Fatalf("bodies of 4 and %d assignments cost %d and %d steps; neither fills the %d node "+
			"window, so both must stay free", wider, small, larger, predeclareScanNodesPerStep)
	}
}
