package runtime

import "github.com/mgomes/vibescript/vibes/value"

// Scoped mutation epochs make the estimator's base-walk memo survive mutations
// that provably cannot change what this execution can reach.
//
// The memo (see beginBaseWalk) is the pair (a committed seen-state, a byte
// total), and it is correct only while nothing in the walked graph has changed.
// Keying it on the process-wide mutation epoch alone made that test far
// coarser than the dependency it stands for: every execution in the process
// advances one counter, so a mutation anywhere discarded every memo everywhere,
// and each discarded memo costs a full re-walk of its own reachable graph. A
// second execution doing constant small writes therefore drove an unrelated
// script's estimator work from linear to quadratic in its receiver -- and it did
// not even need to mutate, because builtin dispatch bumps the epoch too, so a
// loop of pure builtin calls was enough. The walk is deliberately unbilled (see
// outputWalkBytes), so the step quota never intervened.
//
// The split is (execution-private counter, process-wide counter). A write the
// runtime can attribute to one execution bumps only that execution's counter; a
// write it cannot attribute bumps the process-wide one, which invalidates
// everything. A memo is valid while both halves are unchanged.
//
// # Why an execution-private counter is sound
//
// Two Executions in one process do not share mutable state that a base walk
// reaches. The walk covers exec.root, the env stack, the module tail and the
// registered output roots, and each of those is rebuilt per execution:
//
//   - exec.root is allocated fresh for every Script.Call.
//   - Module environments, class clones and class variables are rebuilt per
//     execution per require; the Engine caches only the compiled Script and its
//     AST, which are immutable after compile.
//   - Host arguments, globals and results are deep-copied across the boundary in
//     both directions, so a Value the host holds is never the Value an execution
//     walks (see bindGlobalsForCallLazy and the inbound rebinder).
//   - The one env shared with the Engine is the frozen builtin proto, and the
//     estimator charges a frozen env as a scalar without recursing into its
//     bindings or its parent, so its contents never enter a walked identity set.
//   - Task jobs are the one path that shares state with a parent, and an
//     execution running one carries task globals for its whole call, which
//     routes every check down the memo bypass. The parent bypasses too while any
//     group is active. Neither side has a memo to go stale.
//
// A memo also lives no longer than the call that built it: the estimator is a
// field of the Execution, and a cache recycled through the engine's spare slot
// is invalidated on acquisition. So a host mutating its own values between calls
// has no memo to invalidate, and mutating them during a call is already
// forbidden (see the value package's documentation).
//
// # The direction that is safe to be wrong in
//
// Routing a write to the process-wide counter when it could have been scoped
// costs a memo refresh and nothing else. Routing one to an execution's private
// counter when another execution can observe the mutated state would let that
// execution's memo serve a byte total that omits the change -- an under-count,
// which is the failure this whole accounting system exists to prevent. Every
// site that cannot name the execution it belongs to therefore stays on the
// process-wide counter, and a site added later that forgets to scope is slow
// rather than permissive.

// walkEpoch is the pair of counters a memoized base walk depends on: shared is
// the process-wide mutation epoch, local the private counter of the execution
// the memo belongs to. It is compared as a whole, so a memo survives only while
// both halves are unchanged.
type walkEpoch struct {
	local  uint64
	shared uint64
}

// walkEpoch reads the current memo key for this execution. Callers snapshot it
// before walking, so a bump that lands mid-walk fails the comparison on the next
// session and forces a conservative re-walk.
func (exec *Execution) walkEpoch() walkEpoch {
	return walkEpoch{local: exec.mutationEpoch, shared: value.MutationEpoch()}
}

// bumpMutationEpoch invalidates this execution's memoized estimator base walk.
// Runtime code must call it before any write that changes state reachable from
// this execution's roots and that the value package's own wrapper mutators do
// not already cover: raw slice and map writes (index assignment, instance and
// class variable stores) plus builtin dispatch, which covers every Go builtin's
// internal writes wholesale.
//
// It advances only this execution's counter, leaving every other execution's
// memo intact. That is sound because no other execution can reach this one's
// graph; see the disjointness argument at the top of this file. A write that
// does not have an execution in scope must use value.BumpMutationEpoch instead.
func (exec *Execution) bumpMutationEpoch() { exec.mutationEpoch++ }

// adoptRootEpoch binds this execution's root scope to its private mutation
// counter. Every scope pushed beneath the root inherits the pointer down the
// parent chain (see Env.adoptEpochFrom), so this one assignment is what scopes
// every binding write the call goes on to make. It must run before evaluation
// starts. A root left unbound is not incorrect -- its writes fall back to the
// process-wide counter -- but it invalidates every memo in the process on each
// binding write, which is the cost this scoping exists to remove.
func (exec *Execution) adoptRootEpoch() {
	if exec.root != nil {
		exec.root.epoch = &exec.mutationEpoch
	}
}

// bumpSharedMutationEpoch invalidates every memoized estimator base walk in the
// process. It is the fallback for writes with no execution in scope, where
// attribution is impossible and over-invalidating is the safe answer.
func bumpSharedMutationEpoch() { value.BumpMutationEpoch() }
