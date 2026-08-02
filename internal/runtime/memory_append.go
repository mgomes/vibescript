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
	if len(elems)+1 > cap(elems) {
		// A reallocation abandons the old backing only if nothing else
		// aliases it, which the seen-state cannot know: a host builtin or
		// capability can hold an overlapping view that keeps the old backing
		// reachable after the copy, so neither deleting its identity nor
		// charging only the capacity delta describes the graph. Reallocating
		// appends take the ordinary reserve-and-shovel path, whose epoch bump
		// re-walks and re-counts every backing; a growth loop reallocates
		// O(log n) times, so the charged fast path keeps its linear win. The
		// check runs before the element walk below so a declined append
		// commits nothing into the seen-state.
		return false, nil
	}
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

	used := saturatingAdd(exec.estimateScalarBase(), saturatingAdd(c.graphBytes, marginal))
	if used > exec.memoryQuota {
		c.valid = false
		return true, fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}

	left.AppendArrayElemNoEpoch(right)
	if sliceBackingIdentity(left.Array()) != id {
		// Unreachable for an in-capacity append; if the backing ever moved
		// anyway, the delta model no longer describes the graph.
		c.valid = false
		return true, nil
	}
	c.graphBytes = saturatingAdd(c.graphBytes, marginal)
	c.journalBudget = sessionJournalBudget(est.identityCount())
	exec.verifyChargedCommit("append")
	return true, nil
}

// hashStoreCharged implements `target[key] = val` for a root-reachable typed
// hash adding a new key, the same way appendArrayCharged handles the shovel:
// the entry's marginal bytes are committed into the base-walk memo and the
// write skips the epoch bump, so a loop that fills a hash stays linear under
// the quota (#1129). Replacements fall back — subtracting a replaced value
// from a deduplicated union is not sound — as does anything the append path
// would decline.
func (exec *Execution) hashStoreCharged(target, key, val Value) (handled bool, err error) {
	if exec.memoryQuota <= 0 || exec.baseWalkOpen || exec.builtinDepth > 0 ||
		len(exec.activeTaskGroups) > 0 || exec.blockRegionActive || baseWalkCacheDisabled.Load() {
		return false, nil
	}
	if target.Kind() != KindHash || !hashHasTypedEntries(target) {
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
	id := hashIdentity(target)
	if id == 0 {
		return false, nil
	}
	if _, committed := exec.memoryEst.seenHashData[id]; !committed {
		return false, nil
	}
	lookupKey, keyErr := hashLookupKey(key)
	if keyErr != nil {
		// The ordinary path owns unhashable-key errors.
		return false, nil
	}
	if _, exists, getErr := target.HashGet(key); getErr != nil || exists {
		return false, nil
	}
	legacyEntries, legacyMirrored := hashStringMapIfMaterialized(target)
	if legacyMirrored {
		// A kind-strict add can still collide in the legacy display-key
		// mirror (an int 1 and a string "1" both display as "1"), turning
		// the mirror write into a replacement whose delta cannot be added
		// arithmetically. Leave those to the ordinary path.
		if _, collides := legacyEntries[hashDisplayKey(key)]; collides {
			return false, nil
		}
	}

	// See appendArrayCharged: the walk commits directly, success keeps the
	// memo exact, overflow discards it.
	est := &exec.memoryEst
	est.journal = nil
	est.dormant = exec.currentDormantSet()
	marginal := est.valuePayload(key)
	marginal = saturatingAdd(marginal, est.valuePayload(val))
	marginal = saturatingAdd(marginal, lookupKey.ExtraPayloadBytes())

	// An add past the typed capacity high-water mark realizes one more entry
	// slot; an add below it consumes a slot the walk already prices.
	entryCount := target.HashLen()
	if entryCount+1 > value.HashTypedEntryCapacity(target) {
		marginal = saturatingAdd(marginal, estimatedMapEntryBytes+estimatedHashLookupKeyBytes+estimatedHashEntryBytes)
	}
	// A literal-built hash keeps its legacy string map materialized; HashSet
	// mirrors every write into it, and mapStructuralBytes prices each entry as
	// a bucket, a Value slot, and the display key's header and bytes. The
	// reference walk also visits the mirrored value twice — once through the
	// legacy map's loop and once through the typed entries — and the second
	// visit charges whatever does not deduplicate (a string's header, a
	// regex's source length). Walking the value again over the just-committed
	// state reproduces that residue exactly.
	if legacyMirrored {
		displayKey := hashDisplayKey(key)
		marginal = saturatingAdd(marginal, estimatedMapEntryBytes+estimatedValueBytes+estimatedStringHeaderBytes+len(displayKey))
		marginal = saturatingAdd(marginal, est.valuePayload(val))
	}

	// No transient peak is charged for the order backing's growth: the
	// ordinary epoch-bumping path performs no equivalent reservation for a
	// hash store (unlike the array shovel, whose fallback prices its
	// realloc), so admitting here on the same terms the fallback's post-store
	// walk uses keeps the smallest admitting quota identical whichever path a
	// store happens to take. The realized growth is committed after the
	// write below.
	orderCapBefore := value.HashOrderCapacity(target)
	used := saturatingAdd(exec.estimateScalarBase(), saturatingAdd(c.graphBytes, marginal))
	if used > exec.memoryQuota {
		c.valid = false
		return true, fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}

	if setErr := target.HashSetUnpublished(key, val); setErr != nil {
		// The write itself failed after the walk committed the entry's
		// identities; the memo no longer matches the graph.
		c.valid = false
		return true, setErr
	}
	if orderCapAfter := value.HashOrderCapacity(target); orderCapAfter != orderCapBefore {
		growth := saturatingMul(orderCapAfter-orderCapBefore, estimatedHashLookupKeyBytes)
		if orderCapBefore == 0 {
			growth = saturatingAdd(growth, estimatedSliceBaseBytes)
		}
		marginal = saturatingAdd(marginal, growth)
	}
	c.graphBytes = saturatingAdd(c.graphBytes, marginal)
	c.journalBudget = sessionJournalBudget(est.identityCount())
	exec.verifyChargedCommit("hash store")
	return true, nil
}

// verifyChargedCommit is the differential oracle for the charged-commit
// paths: under VIBES_ESTIMATOR_VERIFY the memoized total must be
// byte-identical to a from-scratch reference walk after every commit.
func (exec *Execution) verifyChargedCommit(op string) {
	if !estimatorVerify {
		return
	}
	c := exec.baseWalkCache
	verify := newMemoryEstimator()
	if reference := exec.estimateGraphBase(verify, nil); reference != c.graphBytes {
		panic(fmt.Sprintf("charged %s diverged: memo holds %d bytes, reference walk %d", op, c.graphBytes, reference))
	}
}
