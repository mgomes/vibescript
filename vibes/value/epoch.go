package value

import "sync/atomic"

// mutationEpoch is a process-wide monotonic counter bumped by every in-place
// mutation of a shared value wrapper (an array's element slice, a hash's
// entries, defaults, insertion order, or capacity metadata) and, through
// BumpMutationEpoch, by any runtime write to reachable state that cannot name
// the execution it belongs to.
//
// The runtime's memory-quota estimator memoizes its reachable-graph walk keyed
// on a pair: this counter and a second counter private to the execution being
// measured. A bump here invalidates every memo in the process, which is always
// correct and never stale, but is far more than a mutation usually needs: an
// execution's reachable graph is rebuilt per call and cannot be reached by any
// other execution, so a write the runtime can attribute to one execution only
// has to invalidate that execution's memo. Those writes bump the private
// counter instead (see the runtime's scoped mutation epoch), and what remains
// here is the conservative residue -- host-visible wrapper mutation, and the
// handful of runtime writes with no execution in scope.
//
// The asymmetry is deliberate and is the safety property of the split: routing
// a write here when it could have been scoped costs a memo refresh, while
// routing one to a private counter when another execution could observe the
// mutated state would let that execution's memo serve a total that omits the
// change. Anything that cannot be attributed belongs here.
var mutationEpoch atomic.Uint64

// MutationEpoch returns the current process-wide mutation epoch. It is one half
// of the estimator's memo key; the other half is private to each execution.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func MutationEpoch() uint64 { return mutationEpoch.Load() }

// BumpMutationEpoch advances the process-wide mutation epoch, invalidating
// every memoized estimator walk in the process. Every code path that mutates
// state reachable by a memory-quota walk -- in this package's wrapper mutators
// and in the runtime -- must invalidate before the mutated state can be
// observed by a check, either through this or, when the write is attributable
// to a single execution, through that execution's own bump.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func BumpMutationEpoch() { mutationEpoch.Add(1) }
