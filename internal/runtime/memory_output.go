package runtime

import (
	"fmt"

	"github.com/mgomes/vibescript/vibes/value"
)

// Output-root accounting closes the hole a builtin opens when it accumulates
// results into a Go local while calling back into script code.
//
// The estimator measures what the interpreter can reach: the environments, the
// modules, the task-group retention. A driver's own result slice is none of
// those. So while Hash#fetch_values, Hash#values_at, hash.map, array.map,
// Hash#map_with_index and Hash#transform_values run, every check performed
// inside the callback -- the block's own allocations, its calls, step()'s
// periodic walk -- measured a graph that omitted everything the loop had
// already retained. A script could return an individually permitted value per
// callback and accumulate past the quota one accepted result at a time.
//
// Registering the output as a base-walk root closes it by construction rather
// than by arithmetic. Every check re-derives what the output holds at the
// moment it runs, through the same deduplicating estimator that walks the
// receiver and the arguments, so a result that aliases something already
// counted is counted once, and a result the callback has just grown, stored, or
// detached is priced as it is now rather than as it was when it was produced.
// That last property is why pricing failed: a callback can grow a retained
// value, detach it from the receiver, and allocate against the difference all
// within one call, so no measurement taken before a callback is still true
// while the callback allocates.
//
// The roots are walked with the graph, committed into the same seen-state, and
// memoized beside it. A driver fills its output with a raw Go write that bumps
// no mutation epoch, so the memo alone would go stale by exactly the results
// the driver adds; each of those is committed through addRetainedOutput, the
// way the charged append commits a shovel's marginal (see memory_append.go).
// Everything else that can change what the output holds is a script-visible
// mutation, which bumps the epoch and discards the memo, so the output is
// re-derived from scratch before the next check reads it. Registering and
// unregistering a root changes the walk's root set, so both bump the topology
// version, exactly as pushing an initializing module's environment does.
//
// Memoizing them is not an optimization to be traded away: a driver runs many
// checks between two changes to its output, so re-walking the roots as session
// extras on every check is quadratic in the output's length. array.map over
// 4000 elements takes 216ms that way against 3.4ms here.

// outputWalkRoot charges the payload a driver's Go-local output currently
// retains, through est so it deduplicates against everything the walk has
// already counted. Only payloads are re-derived: the output's slot backing is a
// fixed allocation whose size is known before the first callback, and the
// drivers reserve it through reserveLoopScratch so the runner's bind baseline
// includes it.
type outputWalkRoot func(est *memoryEstimator) int

// retainedValues walks a result slice the driver fills or appends to. It takes
// a pointer because append can move the backing, and the walk must see the
// slice the driver holds now rather than the header it held at registration.
func retainedValues(out *[]Value) outputWalkRoot {
	return func(est *memoryEstimator) int {
		total := 0
		for _, val := range *out {
			total = saturatingAdd(total, est.valuePayload(val))
		}
		return total
	}
}

// retainedEntryValues walks the transformed values a hash driver stages in an
// entry buffer before publishing them into its result map. Only the values are
// charged: the keys are the receiver's own, which the walk reaches through it.
func retainedEntryValues(out *[]HashEntry) outputWalkRoot {
	return func(est *memoryEstimator) int {
		total := 0
		for _, entry := range *out {
			total = saturatingAdd(total, est.valuePayload(entry.Value))
		}
		return total
	}
}

// retainedMapValues walks a result map the driver fills. Like retainedValues it
// takes a pointer, so a driver can register before it allocates the map and
// keep the registration ahead of the block-call runner, whose bind baseline is
// snapshotted once at construction. Only the values are charged: the keys are
// the receiver's own, which the walk reaches through it.
func retainedMapValues(out *map[string]Value) outputWalkRoot {
	return func(est *memoryEstimator) int {
		total := 0
		for _, val := range *out {
			total = saturatingAdd(total, est.valuePayload(val))
		}
		return total
	}
}

// pushOutputWalkRoot registers a driver's Go-local output for the duration of
// its loop; the driver pairs it with a deferred popOutputWalkRoot. It is a
// no-op without an enforced memory quota, where no estimator walk runs at all.
func (exec *Execution) pushOutputWalkRoot(walk outputWalkRoot) {
	if exec.memoryQuota <= 0 {
		return
	}
	exec.baseTopoVersion++
	exec.outputWalkRoots = append(exec.outputWalkRoots, walk)
}

func (exec *Execution) popOutputWalkRoot() {
	if exec.memoryQuota <= 0 {
		return
	}
	last := len(exec.outputWalkRoots) - 1
	if last < 0 {
		return
	}
	exec.baseTopoVersion++
	// Clear before shortening: a truncated slice keeps the closure -- and
	// through it the whole output -- alive in its backing array, so the values
	// would stay reachable for the collector while the walk, which reads only
	// the visible length, stopped charging them.
	exec.outputWalkRoots[last] = nil
	exec.outputWalkRoots = exec.outputWalkRoots[:last]
}

// outputWalkBytes charges every registered output root through est. Nested
// drivers (a map inside a map's block) each register their own, and they
// deduplicate against one another the way any two roots do.
func (exec *Execution) outputWalkBytes(est *memoryEstimator) int {
	total := 0
	for _, walk := range exec.outputWalkRoots {
		total = saturatingAdd(total, walk(est))
	}
	return total
}

// addRetainedOutput records that val has just joined a registered output, and
// commits its marginal bytes into the memoized base walk so the next check sees
// it without re-walking the whole output. The driver calls it immediately after
// the write that publishes val into its Go local.
//
// A driver adds exactly one value at a time and never replaces one, so the
// deduplicated union the memo holds grows by exactly this value's marginal over
// the committed seen-state -- the same delta model the charged append uses. Any
// other way the output's contents can change is a mutation performed by script
// code, which bumps the mutation epoch and discards the memo. When the memo
// cannot take the commit it is invalidated instead: leaving it valid would
// serve a total that omits this result, which is the under-count the whole
// mechanism exists to prevent.
func (exec *Execution) addRetainedOutput(val Value) {
	if exec.memoryQuota <= 0 || len(exec.outputWalkRoots) == 0 {
		return
	}
	c := exec.baseWalkCache
	if c == nil || !c.valid {
		return
	}
	if exec.baseWalkOpen || c.epoch != value.MutationEpoch() || c.topo != exec.baseTopoVersion ||
		c.regionBoundary != exec.currentWalkBoundary() {
		c.valid = false
		return
	}
	est := &exec.memoryEst
	est.journal = nil
	est.dormant = exec.walkDormantSet(c.regionBoundary)
	c.outputBytes = saturatingAdd(c.outputBytes, est.valuePayload(val))
	c.journalBudget = sessionJournalBudget(est.identityCount())
	exec.verifyRetainedOutputCommit(c)
}

// retainedOutputBytes reports what the registered driver outputs currently
// contribute to the live footprint, for the accounting paths that weigh an
// allocation against a snapshotted baseline rather than against a walk. The
// bind charge behind a block's rest-window preflight is the one that needs it:
// its baseline is taken once, before the driver's first callback, and it used to
// track the retained output through exec.reservedScratchBytes because the
// drivers reserved their results there. They register them now instead, so
// without this a rest window is preflighted against a base that omits every
// result the loop has kept and the fresh backing is allocated before a later
// body check observes the excess.
//
// The memoized total is the same number the checks read, so the fast path is
// exact and O(1). When the memo cannot answer -- something mutated, or the walk
// shape moved -- the roots are walked against an empty estimator, which counts
// their whole footprint rather than their marginal over the graph: an
// over-charge, which is the safe direction for a gate that decides whether to
// allocate.
func (exec *Execution) retainedOutputBytes() int {
	if len(exec.outputWalkRoots) == 0 {
		return 0
	}
	if c := exec.baseWalkCache; c != nil && c.valid && !exec.baseWalkOpen &&
		c.epoch == value.MutationEpoch() && c.topo == exec.baseTopoVersion &&
		c.regionBoundary == exec.currentWalkBoundary() {
		return c.outputBytes
	}
	return exec.outputWalkBytes(newMemoryEstimator())
}

// currentWalkBoundary reports the walk shape the next memory check would take:
// a block-iteration region's prefix boundary, or noBlockRegion for the ordinary
// whole-stack walk. A commit made against a different shape than the memo holds
// would be added to a total the next check discards, so the caller invalidates
// instead.
func (exec *Execution) currentWalkBoundary() int {
	if exec.blockRegionBaseWalkEngaged(taskLazyGlobalsFromContext(exec.Context())) {
		return exec.blockRegionBoundary
	}
	return noBlockRegion
}

// walkDormantSet returns the dormant-frame set the walk that built the memo
// used, so a commit prices an environment reachable from val exactly as that
// walk would have. A region walk does not use the optimization at all.
func (exec *Execution) walkDormantSet(boundary int) map[*Env]struct{} {
	if boundary != noBlockRegion {
		return nil
	}
	return exec.currentDormantSet()
}

// verifyRetainedOutputCommit is the differential oracle for the commit above:
// under VIBES_ESTIMATOR_VERIFY the memoized graph and output totals together
// must be byte-identical to a from-scratch reference walk of the same shape.
func (exec *Execution) verifyRetainedOutputCommit(c *baseWalkCache) {
	if !estimatorVerify {
		return
	}
	ref := newMemoryEstimator()
	var graph int
	if c.regionBoundary == noBlockRegion {
		graph = exec.estimateGraphBase(ref, nil)
	} else {
		graph = exec.estimateGraphBasePrefix(ref, c.regionBoundary, nil)
	}
	reference := graph + exec.outputWalkBytes(ref)
	if memoized := c.graphBytes + c.outputBytes; memoized != reference {
		panic(fmt.Sprintf(
			"vibescript: retained-output commit diverged: memo holds %d bytes (graph=%d output=%d), reference walk %d",
			memoized, c.graphBytes, c.outputBytes, reference))
	}
}
