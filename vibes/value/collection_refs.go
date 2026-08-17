package value

import "sync/atomic"

// Arrays and hashes are values: binding, passing, or returning one produces
// another logical value, and updating one of them cannot change a sibling. The
// runtime implements that with copy-on-write rather than a copy per binding,
// which needs one fact about a wrapper at the moment something writes through
// it -- whether anything else can still see it.
//
// refs is that fact, as a saturating count of the durable slots naming a
// wrapper. A durable slot is somewhere a value outlives the expression that
// produced it: an environment binding, an instance or class variable, an array
// element, a hash entry. Go locals inside the interpreter are not durable; they
// die with the operation holding them.
//
// The count saturates at two because the question is only ever "more than one?",
// and it never decrements: a slot going away leaves an over-count, which costs
// one copy that was not needed and can never lose an alias. Under-counting would
// lose one, so every duplication of a durable handle must publish.
const (
	// refsFresh marks a wrapper no durable slot names yet -- one the operation
	// that built it still solely holds. Writing through it is safe.
	refsFresh uint32 = iota
	// refsSole marks a wrapper exactly one durable slot names. Writing through
	// it is safe: that slot is the value being updated.
	refsSole
	// refsShared marks a wrapper two or more durable slots may name. A write
	// must copy the wrapper first and rebind the slot it reached it through.
	refsShared
)

// PublishRef records that another durable slot now names the collection v, so
// that a later write through any of them copies rather than mutating a value
// something else can still see. It is a no-op for values that carry no wrapper.
//
// Every site that places a collection somewhere outliving the current
// expression calls it: environment binds and assignments, instance and class
// variable stores, hash entry stores, and the array builders that take elements
// they did not construct. Missing one is an aliasing bug, so the runtime's
// always-copy verification mode exists to find one (see docs/collections.md).
//
// It is intended for the interpreter's internal use; hosts should not call it,
// and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) PublishRef() {
	if slot := v.refSlot(); slot != nil {
		advanceRef(slot)
	}
}

// PublishReplacement publishes next unless it is the wrapper previous already
// names, in which case the store duplicates no handle and must not count one.
//
// It is what keeps `items = items.push(x)` linear. Writing a mutator's result
// back over the receiver it updated is the idiom value semantics asks for, and
// counting that store would leave the binding looking shared to the next
// iteration, so every push after the first would copy the whole array.
//
// It is intended for the interpreter's internal use; hosts should not call it,
// and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func PublishReplacement(previous, next Value) {
	slot := next.refSlot()
	if slot == nil || slot == previous.refSlot() {
		return
	}
	advanceRef(slot)
}

// advanceRef moves a wrapper's publication state one step along
// fresh → sole → shared. The transition is a CAS so two concurrent
// publishers cannot both observe fresh and both land on sole, and so a
// MarkSharedRef cannot be overwritten back to sole.
func advanceRef(slot *uint32) {
	for {
		switch current := atomic.LoadUint32(slot); current {
		case refsFresh:
			if atomic.CompareAndSwapUint32(slot, refsFresh, refsSole) {
				return
			}
		case refsSole:
			if atomic.CompareAndSwapUint32(slot, refsSole, refsShared) {
				return
			}
		default:
			return
		}
	}
}

// AdoptSoleRef records that exactly one durable slot names the collection v.
// The copy a write makes when it finds a shared wrapper is installed at the one
// slot the write reached it through, so it starts again from sole ownership and
// the next write through that slot mutates in place. Without it a mutating loop
// would copy on every iteration.
//
// Callers owe the invariant the name states: v must be a wrapper nothing else
// holds, installed at one slot.
//
// It is intended for the interpreter's internal use; hosts should not call it,
// and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) AdoptSoleRef() {
	if slot := v.refSlot(); slot != nil {
		atomic.StoreUint32(slot, refsSole)
	}
}

// Unpublished reports whether no durable slot names the collection v -- that
// it is still solely held by the operation that built it. A write through such
// a wrapper is safe from any route, because nothing else can reach it.
//
// It is intended for the interpreter's internal use; hosts should not call it,
// and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) Unpublished() bool {
	slot := v.refSlot()
	return slot == nil || atomic.LoadUint32(slot) == refsFresh
}

// SoleRef reports whether the collection v may be written through in place --
// that is, whether at most one durable slot names its wrapper. It reports true
// for every value that carries no wrapper, since those have nothing to alias.
//
// It is intended for the interpreter's internal use; hosts should not call it,
// and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) SoleRef() bool {
	slot := v.refSlot()
	return slot == nil || atomic.LoadUint32(slot) < refsShared
}

// PublishRefElems publishes every collection among elems. The array
// constructors call it, since an array adopts elements its caller may still
// hold, and so does every builder that hands NewArray a slice of values it did
// not itself construct.
//
// It is intended for the interpreter's internal use; hosts should not call it,
// and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func PublishRefElems(elems []Value) {
	// The kind test is inlined and the elements are ranged by index because
	// this runs over every array the interpreter builds: copying a 40-byte
	// Value per slot to ask what kind it is showed up as a double-digit
	// regression on the array-building benchmarks.
	for i := range elems {
		switch elems[i].kind {
		case KindArray, KindHash, KindObject:
			elems[i].PublishRef()
		}
	}
}

// PublishRefEntries publishes every collection among a hash's values.
//
// It is intended for the interpreter's internal use; hosts should not call it,
// and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func PublishRefEntries(entries map[string]Value) {
	for _, entry := range entries {
		switch entry.kind {
		case KindArray, KindHash, KindObject:
			entry.PublishRef()
		}
	}
}

// MarkSharedRef forces v to the shared state, so that the next write through it
// copies. The host boundary uses it for the values it hands out and takes in,
// where the interpreter cannot see how many handles exist on the other side.
//
// It is intended for the interpreter's internal use; hosts should not call it,
// and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) MarkSharedRef() {
	if slot := v.refSlot(); slot != nil {
		atomic.StoreUint32(slot, refsShared)
	}
}

// refSlot returns the ref counter of v's wrapper, or nil when v carries none.
//
// The counter is read and written atomically because a value can be reachable
// from state an engine shares across concurrent Script.Call invocations, where
// two executions may publish the same wrapper at once. Publication advances
// with a CAS so the state only moves fresh → sole → shared.
func (v Value) refSlot() *uint32 {
	switch v.kind {
	case KindArray:
		if ad, ok := v.data.(*arrayData); ok {
			return &ad.refs
		}
	case KindHash:
		if hd, ok := v.data.(*hashData); ok {
			return &hd.refs
		}
	case KindObject:
		if od, ok := v.data.(*objectData); ok {
			return &od.refs
		}
	}
	return nil
}
