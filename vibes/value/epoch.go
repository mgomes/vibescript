package value

import "sync/atomic"

// mutationEpoch is a process-wide monotonic counter bumped by every in-place
// mutation of a shared value wrapper (an array's element slice, a hash's
// entries, defaults, insertion order, or capacity metadata) and, through
// BumpMutationEpoch, by the runtime's other reachable-state writes (environment
// bindings, instance variables, class variables, builtin dispatch).
//
// The runtime's memory-quota estimator memoizes its reachable-graph walk keyed
// on this counter: a memo recorded at epoch N is reused only while the epoch is
// still N, so a single bump anywhere invalidates every memo in the process.
// That makes staleness impossible by construction rather than by tracking which
// wrapper changed -- the memo is whole-walk, never per-node, so there is no
// ancestor or alias bookkeeping to get wrong. Over-bumping (a mutator that
// changes nothing) is always safe; it only costs a memo refresh.
var mutationEpoch atomic.Uint64

// MutationEpoch returns the current process-wide mutation epoch.
func MutationEpoch() uint64 { return mutationEpoch.Load() }

// BumpMutationEpoch advances the process-wide mutation epoch, invalidating
// every memoized estimator walk. Every code path that mutates state reachable
// by a memory-quota walk -- in this package's wrapper mutators and in the
// runtime -- must call it before the mutated state can be observed by a check.
func BumpMutationEpoch() { mutationEpoch.Add(1) }
