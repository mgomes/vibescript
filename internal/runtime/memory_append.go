package runtime

import (
	"fmt"

	"github.com/mgomes/vibescript/vibes/value"
)

// appendArrayCharged implements `left << right` for a root-reachable array by
// committing the appended element's marginal bytes into the base-walk memo
// instead of mutating first and invalidating the memo after. The epoch bump a
// plain append performs is correct but maximally pessimistic: it forces the
// next memory check to re-walk the whole reachable graph, which turns a loop
// that grows an array into quadratic work — the dominant remaining term in
// #1129 once the literal-side walks are gone. Here the memo stays valid across
// the mutation because its total is updated by exactly the delta the append
// creates: the element's payload (deduplicated against the committed
// seen-state, so aliases of already-counted data cost nothing) plus any
// backing growth.
//
// handled reports whether the append was performed (or definitively rejected)
// here; false means the caller must take the ordinary reserve-and-shovel path.
// Eligibility is deliberately strict: the memo must exist, be valid, and match
// the current epoch and topology, the receiver's backing must already be part
// of the committed walk (proving the array was reachable when the memo was
// built), and none of the bypass conditions that route beginBaseWalk away from
// the memo may hold. Anything else falls back.
func (exec *Execution) appendArrayCharged(left, right Value) (handled bool, err error) {
	if exec.memoryQuota <= 0 || exec.baseWalkOpen || exec.builtinDepth > 0 ||
		len(exec.activeTaskGroups) > 0 || exec.blockRegionActive || baseWalkCacheDisabled.Load() {
		return false, nil
	}
	if taskLazyGlobalsFromContext(exec.Context()) != nil {
		return false, nil
	}
	c := exec.baseWalkCache
	if c == nil || !c.valid || c.epoch != value.MutationEpoch() ||
		c.topo != exec.baseTopoVersion || c.regionBoundary != noBlockRegion {
		return false, nil
	}
	elems := left.Array()
	id := sliceBackingIdentity(elems)
	if id == 0 {
		return false, nil
	}
	if _, committed := exec.memoryEst.seenSlices[id]; !committed {
		return false, nil
	}

	// The walk below commits directly into the memoized seen-state: no
	// journal, no rollback. On success that is the point — the element joins
	// the reachable graph and later walks deduplicate against it in O(1). On
	// overflow the committed identities describe an element that was never
	// published, so the memo is discarded and the next check re-walks.
	est := &exec.memoryEst
	est.journal = nil
	est.dormant = exec.currentDormantSet()
	marginal := est.value(right)

	newLen := len(elems) + 1
	realloc := newLen > cap(elems)
	used := saturatingAdd(exec.estimateScalarBase(), saturatingAdd(c.graphBytes, marginal))
	if realloc {
		// Old and new backings coexist while append copies; the old is inside
		// graphBytes, so the peak adds the grown backing in full plus the
		// receiver's call-root Value slot — the identical projection
		// arrayReserveInPlaceGrowth charges on the fallback path, so the
		// smallest admitting quota does not depend on which path ran.
		grownCap := max(saturatingMul(cap(elems), 2), newLen)
		used = saturatingAdd(used, saturatingAdd(arraySlotBackingBytes(grownCap), estimatedValueBytes))
	}
	if used > exec.memoryQuota {
		c.valid = false
		return true, fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}

	left.AppendArrayElemNoEpoch(right)
	grown := left.Array()
	if newID := sliceBackingIdentity(grown); newID != id {
		// The append reallocated. Retire the old backing identity so a freed
		// address later reused by a different slice can never be falsely
		// deduplicated, commit the new one, and account the capacity growth
		// the new backing realized.
		delete(est.seenSlices, id)
		est.seenSlices[newID] = struct{}{}
		marginal = saturatingAdd(marginal, saturatingMul(cap(grown)-cap(elems), estimatedValueBytes))
	}
	c.graphBytes = saturatingAdd(c.graphBytes, marginal)
	c.journalBudget = sessionJournalBudget(est.identityCount())
	if estimatorVerify {
		// Differential oracle: the committed total must be byte-identical to
		// a from-scratch reference walk, or the incremental commit is wrong.
		verify := newMemoryEstimator()
		if reference := exec.estimateGraphBase(verify, nil); reference != c.graphBytes {
			panic(fmt.Sprintf("charged append diverged: memo holds %d bytes, reference walk %d", c.graphBytes, reference))
		}
	}
	return true, nil
}
