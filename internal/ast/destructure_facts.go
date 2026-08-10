package ast

// destructureFacts caches the syntactic properties of a destructuring target
// that the evaluator consults on every assignment. They depend only on the
// shape of the target, never on the values being assigned, so they are settled
// once when the parser builds the node.
//
// The evaluator used to recompute them per level per assignment, and the write
// scan recurses over the whole remaining subtree, so a target nested d deep
// cost O(d squared) per assignment with no depth cap on the nesting. Settling
// them here is instead linear in the program: a target's own facts follow from
// its elements plus the facts its nested children already carry, so no level is
// ever rescanned. Keeping them with the program rather than memoizing them per
// call also means no per-call cache grows behind the memory quota (#49).
type destructureFacts struct {
	computed bool
	// binds reports whether any element of this subtree binds a value. An
	// all-discard pattern such as "(*)" binds nothing.
	binds bool
	// writes reports whether any leaf of this subtree assigns into an existing
	// container, which can mutate an aliased right-hand side's backing store.
	writes bool
	// readBack reports whether a container write is followed, in left-to-right
	// execution order, by an element that binds a value. Only that ordering
	// lets a later read observe an earlier write, so it is the only case that
	// needs a defensive snapshot of the right-hand side.
	readBack bool
}

// NewDestructureTarget builds a destructuring target with its syntactic facts
// settled. Every nested target is built before the target that contains it, so
// each node's facts follow from its own elements in one pass.
func NewDestructureTarget(elements []DestructureElement, position Position) *DestructureTarget {
	return &DestructureTarget{
		Elements: elements,
		Position: position,
		facts:    computeDestructureFacts(elements),
	}
}

func computeDestructureFacts(elements []DestructureElement) destructureFacts {
	facts := destructureFacts{computed: true}
	sawWrite := false
	for _, element := range elements {
		binds := destructureElementBinds(element)
		writes := destructureElementWrites(element.Target)
		facts.binds = facts.binds || binds
		facts.writes = facts.writes || writes
		if sawWrite && binds {
			facts.readBack = true
		}
		sawWrite = sawWrite || writes
	}
	return facts
}

// resolvedFacts returns the target's facts, computing them when the node was
// built without them. Only a host that assembles a target by hand reaches that
// path; everything the parser produces carries its facts already.
func (e *DestructureTarget) resolvedFacts() destructureFacts {
	if e.facts.computed {
		return e.facts
	}
	return computeDestructureFacts(e.Elements)
}

// WriteIsReadBack reports whether the target contains a leaf that assigns into
// an existing container followed, in left-to-right execution order, by a leaf
// that reads a value out of the right-hand side. Only that ordering lets a
// later read observe an earlier write's mutation of an aliased right-hand-side
// array, so it is the only case that needs a defensive snapshot.
//
// A write whose only successors discard their window (for example
// "values[0], * = values", where the trailing anonymous rest reads nothing, or
// "values[0], (*) = values", where the nested follower destructures nothing) is
// safe to alias: no surviving read can observe the mutation, so snapshotting it
// would copy the whole backing slice for no observable effect. Plain
// identifiers, ivars, and class vars write to environment or instance slots
// that never alias the array's backing store, so they count only as reads.
func (e *DestructureTarget) WriteIsReadBack() bool { return e.resolvedFacts().readBack }

// BindsAnyValue reports whether at least one element of the target binds a
// value out of the right-hand side.
func (e *DestructureTarget) BindsAnyValue() bool { return e.resolvedFacts().binds }

// WritesIntoContainer reports whether any leaf of the target assigns into an
// existing container slot.
func (e *DestructureTarget) WritesIntoContainer() bool { return e.resolvedFacts().writes }

// destructureElementBinds reports whether an element binds at least one value
// out of the right-hand side. The anonymous rest ("*") has a nil target and
// discards its window without observing any value, so it never binds. A nested
// destructure binds only if at least one of its own elements does: an
// all-discard pattern such as "(*)" binds nothing, so it must be treated like
// the anonymous rest and not force a defensive snapshot.
func destructureElementBinds(element DestructureElement) bool {
	if element.Target == nil {
		return false
	}
	if nested, ok := element.Target.(*DestructureTarget); ok {
		return nested.BindsAnyValue()
	}
	return true
}

// destructureElementWrites reports whether a leaf (or any nested leaf) assigns
// into an existing container slot.
func destructureElementWrites(target Expression) bool {
	switch leaf := target.(type) {
	case *IndexExpr, *MemberExpr:
		return true
	case *DestructureTarget:
		return leaf.WritesIntoContainer()
	}
	return false
}
