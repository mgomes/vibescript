package runtime

import (
	"context"
	"fmt"
	"runtime"
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
// completes, which is the observable total of everything the run charged. cfg
// carries the rest of the configuration, so a test can pin what the step charge
// must not depend on; its StepQuota is the search variable.
func minStepQuotaToComplete(t *testing.T, cfg Config, src string, arg Value, n, hi int) int {
	t.Helper()

	lo := 1
	for lo < hi {
		mid := (lo + hi) / 2
		cfg.StepQuota = mid
		script := compileScriptWithConfig(t, cfg, src)
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

// Sessions are unavailable while a builtin is on the call stack, so the
// snapshot mode's whole-graph walk survives for those contexts and has to be
// paid for rather than avoided. The charge is what let
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

	cfg := Config{MemoryQuotaBytes: 64 << 20}
	atSmall := minStepQuotaToComplete(t, cfg, src, loopMemoArray(small), iterations, 1<<20)
	atLarge := minStepQuotaToComplete(t, cfg, src, loopMemoArray(large), iterations, 1<<20)

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

// coldMemoHashLiteralSource evaluates the literal inside a called function, so
// every iteration pushes a distinct environment and bumps the root-set topology
// version. That invalidates the base-walk memo before the accumulator's first
// check, which is the case the discarded liveBase session hid.
const coldMemoHashLiteralSource = "def mk()\n  {a: 1, b: 2, c: 3, d: 4, e: 5, f: 6, g: 7, h: 8}\nend\n\n" +
	"def run(a, n)\n  t = 0\n  j = 0\n  while j < n\n    t = mk().length\n    j = j + 1\n  end\n  t\nend"

// Sessions mode's quota check opened a base-walk session through liveBase and
// then threw it away for a second one through sessionUsedBytes. When the memo
// was cold the discarded session paid the whole-graph walk and warmed the memo,
// so the session that was actually charged reported only its cheap replay: the
// walk was both wasted and free. A loop whose literals each invalidate the memo
// therefore re-walked an arbitrarily large host graph for a flat step count --
// 611 steps whether the retained array held 100 elements or 10,000 (#1).
func TestSessionHashLiteralChargesAColdBaseWalk(t *testing.T) {
	const iterations, small, large = 20, 100, 10000
	cfg := Config{MemoryQuotaBytes: 64 << 20}

	atSmall := minStepQuotaToComplete(t, cfg, coldMemoHashLiteralSource, loopMemoArray(small), iterations, 1<<21)
	atLarge := minStepQuotaToComplete(t, cfg, coldMemoHashLiteralSource, loopMemoArray(large), iterations, 1<<21)

	// Half the ideal charge leaves room for the iterations whose memo survives
	// and for the remainder each stepN rounds away.
	want := atSmall + iterations*(large-small)/estimatorNodesPerStep/2
	if atLarge < want {
		t.Errorf("the same literal cost %d steps over a %d-element retained array and %d over a "+
			"%d-element one; want at least %d, one step per %d nodes the cold base walk visits",
			atSmall, small, atLarge, large, want, estimatorNodesPerStep)
	}
}

// hashLiteralRewriteSource writes every key once and then rewrites all of them,
// so the accumulator spends the second half in replacement mode against a
// full-width entry map. That is the shape the replay rebuild was quadratic in.
func hashLiteralRewriteSource(pairs int) string {
	parts := make([]string, 0, 2*pairs)
	for pass := range 2 {
		for i := range pairs {
			parts = append(parts, fmt.Sprintf("k%d: %d", i, i+pass))
		}
	}
	return "def run(a, n)\n  t = 0\n  j = 0\n  while j < n\n    t = ({" +
		strings.Join(parts, ", ") + "}).length\n    j = j + 1\n  end\n  t\nend"
}

// literalBuildBytes reports the bytes a run allocates in total. Allocation is a
// poor proxy for walk complexity, which is why the scaling tests count estimator
// visits instead, but here the transient buffer IS what is being measured, and
// TotalAlloc is cumulative and exact rather than a live-heap sample.
func literalBuildBytes(t *testing.T, src string, iterations int) uint64 {
	t.Helper()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 64 << 20, StepQuota: Unlimited}, src)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := script.Call(context.Background(), "run", []Value{loopMemoArray(100), NewInt(int64(iterations))}, CallOptions{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// The accumulator mirrored the builder's canonical-key entry map into a replay
// slice of its own and rebuilt that slice from scratch on every replacement.
// Neither buffer is reachable from an environment, so no quota projection could
// see either, and the width cap was the only thing bounding them -- removing it
// made them unbounded. A literal that writes k keys and then rewrites them all
// allocated O(k squared) transient bytes: 20 evaluations of a 256-pair one
// allocated 219,517,016 bytes and quadrupled every time the width doubled.
// Replaying from the builder's map instead removes both buffers, so quadrupling
// the width must now roughly quadruple the allocation (#1).
//
// Not parallel: TotalAlloc is process wide.
func TestHashLiteralReplayDoesNotMirrorTheEntryMap(t *testing.T) {
	const iterations, narrow, wide = 20, 64, 256

	atNarrow := literalBuildBytes(t, hashLiteralRewriteSource(narrow), iterations)
	atWide := literalBuildBytes(t, hashLiteralRewriteSource(wide), iterations)

	// Linear growth is 4x for a 4x width. Allow generous headroom so the bound
	// tracks the complexity class, while staying far below the 15x a quadratic
	// rebuild produced.
	if atWide > atNarrow*8 {
		t.Errorf("a %d-pair rewrite allocated %d bytes and a %d-pair one %d; quadrupling the width "+
			"should roughly quadruple the allocation, so the accumulator is still mirroring and "+
			"rebuilding a replay buffer per pair", narrow, atNarrow, wide, atWide)
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
