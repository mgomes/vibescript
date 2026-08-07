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

// TestPredeclareScanCountsTheWrappersItStepsOver pins that a walk counts the
// branch and rescue-clause wrappers it steps over, not only the statements
// inside them.
//
// A clause whose body is small is mostly wrapper, so a long clause list costs
// real walking that the statement count alone barely sees.
func TestPredeclareScanCountsTheWrappersItStepsOver(t *testing.T) {
	t.Parallel()

	const branches = 32

	elseIf := make([]*IfStmt, 0, branches)
	rescues := make([]RescueClause, 0, branches)
	for range branches {
		elseIf = append(elseIf, &IfStmt{})
		rescues = append(rescues, RescueClause{})
	}

	for _, tc := range []struct {
		name string
		stmt Statement
	}{
		{name: "elsif branches", stmt: &IfStmt{ElseIf: elseIf}},
		{name: "rescue clauses", stmt: &TryStmt{Rescues: rescues}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var collector localBindingCollector
			collectLocalBindingNames([]Statement{tc.stmt}, &collector)
			// The statement itself, plus one per wrapper. The bodies are empty,
			// so anything less means the wrappers walked for free.
			if want := branches + 1; collector.visited != want {
				t.Fatalf("walking %d empty %s counted %d nodes, want %d",
					branches, tc.name, collector.visited, want)
			}
		})
	}
}

// TestPredeclareScanCarriesWhatDoesNotFillAStep pins that walks too small to
// reach the amortization window accumulate instead of rounding away.
//
// Each rescue clause is scanned on its own, so a long clause list is many small
// walks rather than one large one. Truncating each of them independently would
// charge nothing at all however many there were.
func TestPredeclareScanCarriesWhatDoesNotFillAStep(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 1 << 30}
	for range predeclareScanNodesPerStep * 4 {
		exec.chargePredeclareScan(1)
	}
	if want := 4; exec.steps != want {
		t.Fatalf("%d single-node walks cost %d steps, want %d: a walk under the %d node window "+
			"must carry rather than round away", predeclareScanNodesPerStep*4, exec.steps, want,
			predeclareScanNodesPerStep)
	}
}
