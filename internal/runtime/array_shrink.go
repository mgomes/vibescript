package runtime

// Ruby's in-place array shrinks -- Array#pop and Array#shift -- narrow the
// receiver's window over its element backing. Two properties have to hold at
// once, and the obvious implementation of either breaks the other.
//
// The removed elements must stop being reachable from the receiver. Go keeps a
// backing array live as a whole while any slice into it is live, and the memory
// estimator charges an array's structure by capacity but recurses only into the
// visible len(values) range, so an element left in a vacated slot is retained
// and uncounted: a megabyte string removed from each of 200 arrays held 192 MiB
// under an 8 MiB quota (#22). Zeroing the vacated slots fixes that.
//
// The elements a driver already captured must keep their values. A driver
// snapshots the element header before it runs any script and walks that header
// while the script runs, which is the iteration convention
// TestArrayMutationDuringIteration pins. That is every block-driving array
// member function, and it is also the evaluator's `for x in a`, which is not
// dispatched as a builtin at all. Zeroing a slot the driver has not reached yet
// hands it a nil that was never in the array: `a.each { a.pop }` over [1, 2, 3]
// yielded 1, 2, nil. Copying the survivors onto a fresh backing fixes that
// instead, by leaving the captured header untouched.
//
// Copying on every shrink would also be correct, but it makes a shrink O(len)
// and a drain loop O(n^2). So the shrink copies only when it would otherwise
// write into a backing an enclosing frame is holding, and zeroes in place the
// rest of the time. The common shapes settle out as:
//
//   - `while a.size > 0; a.pop; end` -- no enclosing frame holds the backing,
//     so every pop zeroes in place and the loop stays linear.
//
//   - `a.each { a.pop }`, and `for x in a; a.pop; end` -- the first pop copies,
//     because the driver is holding the header it is still walking. That copy
//     is the receiver's new backing, which the driver is not walking, so every
//     later pop zeroes in place and the loop stays linear.
//
// A copy still bills its own elements, because nothing else in the shrink is
// proportional to them: a builtin that yields once for a flat cost could
// otherwise force an arbitrarily large copy per call. It copies element slots
// rather than payloads, so what it bills the memory quota is a second slot
// array and not a second copy of what the array holds.

// heldArrayBacking is one frame's claim on an array element backing: the
// identity of the backing the frame captured, and the builtin depth it walks
// that header at. The depth is what lets a shrink tell an enclosing frame's
// claim from the claim its own dispatch just recorded for it.
type heldArrayBacking struct {
	id    uintptr
	depth int
}

// holdArrayBackings claims the element backing of every array a frame is about
// to hold across the script it runs, and returns the mark releaseArrayBackings
// takes to drop them again.
//
// A frame's receiver is the obvious one, but not the only one: a global builtin
// and a capability method are dispatched with no receiver at all and drive their
// block from an array argument, so `driver.walk(a) { a.pop }` walked a header
// nothing had claimed. Keyword arguments reach a frame the same way.
//
// The claims are taken where the driver captures the headers it will walk --
// builtin dispatch for the member functions and adapters, the loop head for
// `for x in a` -- so nothing has run in between that could have moved a value
// onto a different backing.
func (exec *Execution) holdArrayBackings(receiver Value, args []Value, kwargs map[string]Value) int {
	mark := len(exec.heldArrayBackings)
	exec.holdArrayBacking(receiver)
	for _, arg := range args {
		exec.holdArrayBacking(arg)
	}
	for _, val := range kwargs {
		exec.holdArrayBacking(val)
	}
	return mark
}

func (exec *Execution) holdArrayBacking(val Value) {
	if val.Kind() != KindArray {
		return
	}
	id := sliceBackingIdentity(val.Array())
	if id == 0 {
		return
	}
	exec.heldArrayBackings = append(exec.heldArrayBackings,
		heldArrayBacking{id: id, depth: exec.builtinDepth})
}

// releaseArrayBackings drops every claim taken since mark.
func (exec *Execution) releaseArrayBackings(mark int) {
	if mark >= len(exec.heldArrayBackings) {
		return
	}
	exec.heldArrayBackings = exec.heldArrayBackings[:mark]
}

// arrayBackingHeldByCaller reports whether a frame enclosing the running one is
// holding values' backing, and so may still read slots this frame is about to
// vacate. The running frame's own claim, recorded by its dispatch, sits at the
// current builtin depth and is not an enclosing one.
func (exec *Execution) arrayBackingHeldByCaller(values []Value) bool {
	if exec == nil || len(exec.heldArrayBackings) == 0 {
		return false
	}
	id := sliceBackingIdentity(values)
	if id == 0 {
		return false
	}
	for _, held := range exec.heldArrayBackings {
		if held.id == id && held.depth < exec.builtinDepth {
			return true
		}
	}
	return false
}

// shrinkArray narrows the receiver to arr[start:end] in place, leaving the
// elements outside that window unreachable from it. pendingSlots is the size of
// any slot array the caller has already allocated but not yet published (the
// removed elements pop(n) and shift(n) return), so a copy is priced against the
// whole live peak rather than against the receiver alone.
func shrinkArray(exec *Execution, receiver Value, arr []Value, start, end int,
	args []Value, kwargs map[string]Value, block Value, pendingSlots int) error {
	window := arr[start:end]
	if !exec.arrayBackingHeldByCaller(arr) {
		clear(arr[:start])
		clear(arr[end:])
		setArrayElems(receiver, window)
		return nil
	}
	if err := exec.chargeScanSteps(len(window)); err != nil {
		return err
	}
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	if err := acc.reserveSlotArrays(len(window), pendingSlots); err != nil {
		return err
	}
	detached := make([]Value, len(window))
	copy(detached, window)
	setArrayElems(receiver, detached)
	return nil
}
