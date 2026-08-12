package runtime

import (
	"context"
	"sync/atomic"
)

// memoryChain bounds the live memory of one chain of nested calls, so that the
// host's memory quota is not handed out again in full to every nesting level.
//
// Each nested task level runs on an Execution of its own, and each Execution
// read MemoryQuotaBytes fresh from the engine config. Live memory therefore
// multiplied with nesting depth while every individual level looked permitted:
// measured at 63 slotted levels holding 130 MiB against an 8 MiB quota, 16x
// what the host configured, refused by nothing. The inline and slotted shapes
// were byte for byte identical at equal depth, so the multiplication follows
// nesting rather than inlining and bounding inline depth alone cannot fix it.
//
// The obvious parallel is sleepBudget, which spans the call tree for the same
// underlying reason. It does not transfer, and the difference is what shapes
// this type. Sleeping is a monotonic spend of a conserved quantity: time given
// to one level is gone, totals are additive, and there is a discrete moment to
// charge. Memory here is none of those. It is a high-water measure of a
// reachable graph, recomputed by a deduplicating walk at every check, which
// must be free to fall again when values die -- the exhaustion latch exists
// precisely because it does. A chained accumulator would ratchet to refusal
// within a few checks on a script whose footprint never grew.
//
// So a level publishes its current footprint rather than spending from an
// allowance, and a check compares the chain's live total against one ceiling.
//
// The second difference decides what a level publishes. Levels' graphs overlap:
// globals and modules are reachable from all of them. Summing whole estimates
// counts that overlap once per level -- measured at 17x for a 4 MiB global
// touched at seventeen levels. So a level publishes only its marginal
// footprint, what it holds beyond the structure it inherited, and the chain
// root publishes its estimate whole. Shared structure is then counted exactly
// once, at the root.
//
// Scope is the ancestor chain, not the whole tree. Depth is the axis nothing
// bounded; width is already bounded by MaxTaskConcurrency, which a host chooses
// deliberately. Aggregating siblings as well would bound width too, but it
// would refuse a 64-wide map of 1 MiB workers that works today -- measured at
// 256 MiB charged for a script that really peaks at 4 MiB. That is a public API
// question about a tree-level ceiling, not a side effect of this fix.
//
// Chain scope is also why nothing has to be torn down. A level sums only its
// own ancestors, so a finished sibling was never in anyone's total and a
// finished child leaves its parent's total untouched. There is no registry to
// deregister from and no teardown hook to miss.
type memoryChain struct {
	parent *memoryChain
	// marginal is what this level currently holds beyond what it inherited,
	// or, at the chain root, its whole estimate. Stored atomically because
	// concurrent siblings sum a shared ancestor on every check of their own,
	// and a lock there would serialize the hot path.
	marginal atomic.Int64
	// limit is the tightest ceiling in the chain, resolved once at
	// construction. Like sleepBudget it is decided on the chain's fixed
	// limits and never on what is left of them: an allowance moves in both
	// directions as levels publish and finish, so a decision made on a
	// momentarily low total would be undone by the next check.
	limit int64
}

// publishAndExceeds records this level's current footprint and reports whether
// the chain now exceeds its ceiling.
//
// A negative contribution is clamped to zero rather than credited. The marginal
// is a difference between two independent graph walks, and a level whose own
// graph has shrunk below the baseline it entered with would otherwise hand its
// ancestors allowance the host never granted.
func (c *memoryChain) publishAndExceeds(marginal int) bool {
	if marginal < 0 {
		marginal = 0
	}
	c.marginal.Store(int64(marginal))
	return c.total() > c.limit
}

// total sums what every level from here to the chain root currently holds.
func (c *memoryChain) total() int64 {
	total := int64(0)
	for node := c; node != nil; node = node.parent {
		total += node.marginal.Load()
	}
	return total
}

// initForCall links this node to the node its caller published on the context
// and resolves its ceiling, reporting whether the node is active at all.
//
// The node lives inside the Execution rather than being allocated separately:
// a chain node per execution cost one allocation per task job, measured at +42
// allocations per operation on a forty-item flat map, which is the shape this
// change must not move. The parent execution outlives every child that points
// at its node, because it is blocked waiting for them.
//
// This reads the context and never wraps it. Publishing is left to whatever
// creates the nesting, because the context a call receives cannot be wrapped
// here: taskLazyGlobalsFromContext identifies its context by a type assertion
// on the outermost value rather than through ctx.Value, so any wrapping applied
// after it silently hides a task's inherited globals. Wrapping here made nested
// tasks fail with "undefined variable" -- a memory-quota concern quietly
// breaking global inheritance -- which is why the two steps are separated.
func (c *memoryChain) initForCall(ctx context.Context, quota int) bool {
	parent := memoryChainFromContext(ctx)
	if quota <= 0 {
		// An engine with no quota of its own still belongs to a bounded
		// caller's chain. Dropping the inherited node here would make
		// re-entering an unlimited engine the way out of the caller's sandbox,
		// exactly as it would for the sleeping budget.
		if parent == nil {
			return false
		}
		c.parent = parent
		c.limit = parent.limit
		return true
	}
	limit := int64(quota)
	if parent != nil && parent.limit < limit {
		limit = parent.limit
	}
	c.parent = parent
	c.limit = limit
	return true
}

type memoryChainKey struct{}

// contextWithMemoryChain publishes a node as the parent of every chain built by
// the calls this context drives.
//
// It is called where the nesting is created -- where a task group captures the
// context its workers will run under -- rather than lazily at the first
// allocation, for the same reason the sleeping budget is established early: a
// group captures its context before any worker runs, so a node published later
// would arrive after the group already held a context without one, and every
// worker would start a chain of its own. That is the defect.
func contextWithMemoryChain(ctx context.Context, chain *memoryChain) context.Context {
	if chain == nil {
		return ctx
	}
	return context.WithValue(ctx, memoryChainKey{}, chain)
}

func memoryChainFromContext(ctx context.Context) *memoryChain {
	if ctx == nil {
		return nil
	}
	chain, _ := ctx.Value(memoryChainKey{}).(*memoryChain)
	return chain
}
