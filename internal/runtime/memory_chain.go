package runtime

import (
	"context"
	"math"
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
// Chain scope keeps siblings out of one another's totals: a level sums only its
// own ancestors, so a finished sibling was never in anyone's figure.
//
// It does not, however, mean nothing has to be torn down. A check has to see
// what is live below it as well as above -- an ancestor growing under a blocked
// descendant is the ordinary slotted shape -- so a node carries a figure derived
// from its descendants, and that figure has to be dropped when they finish or a
// completed chain would bind its ancestors forever. Retirement is therefore real
// work, and it is ordered by what is still running rather than by depth: a level
// can return while a call it started through a capability is still going.
//
// noDescendantConstraint is the headroom of a node with nothing live below it:
// no descendant restricts what it and its ancestors may hold, so only its own
// limit applies.
const noDescendantConstraint = math.MaxInt64

type memoryChain struct {
	parent *memoryChain
	// marginal is what this level currently holds beyond what it inherited,
	// or, at the chain root, its whole estimate. Stored atomically because
	// concurrent siblings sum a shared ancestor on every check of their own,
	// and a lock there would serialize the hot path.
	marginal atomic.Int64
	// descendantHeadroom is the most this node and its ancestors may hold
	// between them without breaching a live chain below it, or
	// noDescendantConstraint when nothing below constrains them.
	//
	// It is one number rather than a pair, and that is the point. The bytes a
	// descendant holds and the ceiling those bytes run under only ever matter
	// combined: a path is refused when prefix + bytesBelow exceeds the deepest
	// limit on it, which rearranges to prefix > deepestLimit - bytesBelow. That
	// difference is the whole constraint a descendant places on its ancestors.
	//
	// Keeping the two separately was a correctness problem, not just a verbose
	// one. They have to move together, they were updated by independent atomics,
	// and every ordering between them was a window in which an ancestor read a
	// stale ceiling against fresh bytes. Two review rounds produced two races of
	// exactly that shape. One value has no such window, and needs no
	// synchronization between parts that no longer exist.
	//
	// The collapse is only sound because initForCall resolves each limit against
	// its parent's, so limits never rise going down a path and the deepest node's
	// limit is the tightest on it.
	descendantHeadroom atomic.Int64
	// liveDescendants counts every level still running anywhere below this node,
	// not just its immediate children, which is what tells headroom whether the
	// constraint below it still stands for anything.
	//
	// Transitive because retirement is not ordered by depth. A level can return
	// while a call it started through a capability is still running, so counting
	// only immediate children let a grandparent see itself as idle and drop a
	// constraint that a live great-grandchild still justified. "Nothing below me
	// is live" is the predicate the clearing actually needs, so it is the one
	// that is counted.
	liveDescendants atomic.Int32
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

	// What this level and everything live below it leaves for its ancestors.
	headroom := c.headroom()

	// One upward pass does both jobs: it sums the ancestors into this check, and
	// it tells each ancestor how little room the chain below it has left.
	//
	// Without the second, the ceiling held in one direction only -- a check
	// walking toward the root cannot see a level below it, so a parent that grew
	// while its child was still holding memory was admitted against a total that
	// omitted the child. Measured at 7.15 MiB live against a 4 MiB ceiling.
	//
	// acc is the bytes strictly below each node as the walk reaches it, and ends
	// as this node's prefix: itself plus every ancestor.
	acc := int64(marginal)
	for node := c.parent; node != nil; node = node.parent {
		node.tightenHeadroom(headroom - acc)
		acc += node.marginal.Load()
	}
	return acc > headroom
}

// ancestorMarginals sums what every level above this one is currently holding.
//
// A budget is not the ceiling: what an ancestor already holds is room this level
// cannot have. Sizing against the ceiling alone let a nested call allocate
// nearly the whole allowance while an ancestor was already consuming most of it.
func (c *memoryChain) ancestorMarginals() int64 {
	total := int64(0)
	for node := c.parent; node != nil; node = node.parent {
		total += node.marginal.Load()
	}
	return total
}

// headroom reports the most this node's prefix -- itself and its ancestors --
// may hold: its own ceiling, or whatever tighter room a live chain below it has
// left, whichever binds first.
func (c *memoryChain) headroom() int64 {
	limit := c.limit
	// A constraint from below applies only while something below is live.
	// Reading the count here rather than clearing the value on the way out is
	// what removes an entire class of race: there is no moment at which one
	// goroutine decides the node is idle and then writes, so nothing can
	// register in between and have its constraint discarded.
	if c.liveDescendants.Load() == 0 {
		return limit
	}
	if below := c.descendantHeadroom.Load(); below < limit {
		limit = below
	}
	return limit
}

// tightenHeadroom records that a chain below this node leaves it at most value.
//
// A minimum over the paths below, never a sum of them. Siblings run concurrently
// but a chain is one path through the tree, so constraining a level by the
// tightest chain beneath it bounds depth without letting the width of a flat map
// aggregate -- the distinction this whole design rests on.
//
// It only ever tightens while children are live, and that is a decision rather
// than an oversight. Relaxing it exactly needs the loosest of the other
// children, which is not knowable without walking them, and walking siblings is
// the width traversal this design exists to avoid. Relaxing it on the strength
// of a "there is only one child" observation was tried and withdrawn: the
// observation races with a second child registering, and losing that race
// overstates the room left, which is the direction that lets a ceiling be
// exceeded. A conservative figure that occasionally leaves too little room is
// worth more here than an exact one that occasionally leaves too much.
//
// The residual is that a child which grows, releases and keeps running holds its
// ancestors to its tightest moment until the last of that ancestor's children
// exits. Bounded by the deepest path actually reached, and in the safe
// direction: it can refuse work that would have fit, never admit work that does
// not.
func (c *memoryChain) tightenHeadroom(value int64) {
	for {
		current := c.descendantHeadroom.Load()
		if current <= value {
			return
		}
		if c.descendantHeadroom.CompareAndSwap(current, value) {
			return
		}
	}
}

// register records this node as live beneath every one of its ancestors, so
// that none of them can forget what is below it while it runs.
//
// It writes nothing but the count. An earlier version also dropped whatever a
// finished generation had left, so a stale constraint could not carry into the
// next one -- and that reset was the fourth spelling of the same race: one
// registrant could observe the count go from none to one, pause while a second
// registrant published something tighter, and then clear that fresh value as
// though it were stale.
//
// It is gone rather than synchronized, on the argument that retired the three
// before it. A stale constraint is *tighter* than the new generation needs, so
// carrying it refuses work that would have fit; the reset wiping a live
// constraint is under-constraint, which is how a ceiling gets exceeded. The
// reset was therefore an optimization defending against the safe error at the
// price of creating the unsafe one.
//
// The residual is that a long-lived node's constraint can ratchet down across
// generations of children, bounded by the tightest path any of them needed, and
// ignored entirely whenever nothing below is live. Conservative, and the fourth
// time on this branch that deleting an optimization beat guarding it.
func (c *memoryChain) register() {
	for node := c.parent; node != nil; node = node.parent {
		node.liveDescendants.Add(1)
	}
}

// release retires a finished level: it stops contributing its own bytes, and
// stops counting as live beneath its ancestors.
//
// It clears no descendant accounting, its own or anyone's. A level can return
// while a call it started through a capability is still running, so clearing
// on the way out erased a live callee's constraint; and clearing on behalf of
// an ancestor meant deciding the ancestor was idle and then writing, which
// races with a replacement child registering in between. Neither is needed:
// a constraint is ignored while nothing below is live, and dropped by the next
// generation's first arrival.
func (c *memoryChain) release() {
	c.marginal.Store(0)
	for node := c.parent; node != nil; node = node.parent {
		node.liveDescendants.Add(-1)
	}
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
	limit := int64(quota)
	if quota <= 0 {
		// An engine with no quota of its own still belongs to a bounded
		// caller's chain. Dropping the inherited node here would make
		// re-entering an unlimited engine the way out of the caller's sandbox,
		// exactly as it would for the sleeping budget.
		if parent == nil {
			return false
		}
		limit = parent.limit
	} else if parent != nil && parent.limit < limit {
		limit = parent.limit
	}
	c.parent = parent
	c.limit = limit
	// A node with nothing below it is bound only by its own ceiling. The zero
	// value would mean the opposite -- no room at all -- so it is set here, on
	// the one path that brings a node into use.
	c.descendantHeadroom.Store(noDescendantConstraint)
	// Registered on the single path out, for every kind of callee. Registering
	// only in the bounded branch left an unlimited callee unregistered, so its
	// ancestors believed nothing was live below them: they then cleared what was
	// live below and this level's footprint never reached any of them.
	c.register()
	return true
}

// newMemoryChain builds a node outside a call, for tests. The zero value of
// descendantHeadroom means "no room", not "no constraint", so a node must never
// be built by struct literal alone.
func newMemoryChain(parent *memoryChain, limit int64) *memoryChain {
	c := &memoryChain{parent: parent, limit: limit}
	c.descendantHeadroom.Store(noDescendantConstraint)
	return c
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
