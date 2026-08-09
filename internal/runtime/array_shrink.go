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
//   - `driver.walk(a) { a.pop }` for a host-driven driver -- no pop writes
//     anything, the array just shows less of the same storage each time, and
//     the drop of the driver's claim clears what it gave up.
//
// A copy still bills its own elements, because nothing else in the shrink is
// proportional to them: a builtin that yields once for a flat cost could
// otherwise force an arbitrarily large copy per call. It copies element slots
// rather than payloads, so what it bills the memory quota is a second slot
// array and not a second copy of what the array holds.
//
// A copy also leaves the holder as the only thing keeping the old header alive,
// which takes it out of reach of every root the estimator walks. The claim
// takes the header over for exactly that reason: it keeps costing until the
// frame holding it returns, so a block cannot drain its receiver, drop the
// result, and build a second generation of the same size against a quota that
// has forgotten the first.
//
// Copying is not enough for a frame the runtime did not write, though, because
// such a frame can be handed the copy back -- as a callback result, or by
// reading a container the script stored the array into -- and then walks
// storage that did not exist when it started. Beneath one of those the shrink
// writes nothing at all: it leaves the storage as it is and narrows the array
// over it, charges the whole header until the claim drops, and clears the
// vacated slots then, when the frame that might have been walking them is done.
// That is the cheapest of the three as well as the safest, since a drain
// narrows the same storage over and over and never copies.

// heldArrayBacking is one frame's claim on the array element headers it walks
// across the script it runs, and the builtin depth it walks them at. The depth
// is what lets a shrink tell an enclosing frame's claim from the claim its own
// dispatch just recorded for it.
//
// A frame the runtime wrote names the one backing it captured. A frame the
// runtime did not write -- a capability method, or a builtin registered through
// Engine.RegisterBuiltin -- cannot say what it captured: it may walk an array
// reached through a hash, an object, or an outer array it was handed, so
// `driver.walk({items: a}) { a.pop }` walked a header no named claim covered.
// Those frames claim every backing instead.
//
// detached holds the headers a shrink copied away from, which the frame still
// walks and no root the estimator walks reaches any more. retained holds the
// headers a shrink left in place beneath a wildcard claim, which the frame may
// be walking and whose vacated slots are cleared once the claim drops.
type heldArrayBacking struct {
	id       uintptr
	depth    int
	wildcard bool
	detached [][]Value
	retained []retainedArrayBacking
	overflow int
}

// retainedArrayBacking is an array a shrink narrowed beneath a wildcard claim
// without touching its storage: the header as it stood when the claim first saw
// it, and the array it belongs to.
//
// A wildcard frame may be walking any of it, so nothing may be written while the
// claim is live -- not the vacated slots, and not by moving the array onto a
// copy, since the frame can be handed the copy back. The whole header is charged
// meanwhile, which is what keeps the vacated payloads on the quota, and the
// vacated slots are cleared when the claim drops and the frame is done.
type retainedArrayBacking struct {
	full     []Value
	receiver Value
}

// maxHeldArrayHeaders caps the headers one claim accounts for by walking them.
//
// A named claim orphans at most one, since the receiver moves off the backing
// the claim names. A wildcard claim has no such bound: it matches every array
// shrunk beneath it, including the ones the script builds and drops inside the
// callback, and walking a list that grows with the callback would make each
// memory check cost what the callback has done so far -- a loop shrinking 8000
// short-lived arrays inside one host call took 8s for 24k charged steps. Past
// the cap a header is charged as a flat reserved total instead, which costs
// nothing per check and over-counts rather than under-counts, since it cannot
// deduplicate against what the live graph already holds.
const maxHeldArrayHeaders = 32

// claims reports whether this frame may be walking the backing identified by
// id. A wildcard frame may be walking any of them: storage created since the
// frame started is no exception, because the frame can be handed it back as a
// callback result or read it out of a container the script stored it in.
func (held *heldArrayBacking) claims(id uintptr) bool {
	return held.wildcard || held.id == id
}

// recordDetached takes over the accounting for a header this claim's frame is
// left holding alone.
func (held *heldArrayBacking) recordDetached(exec *Execution, values []Value) {
	if len(held.detached) < maxHeldArrayHeaders {
		held.detached = append(held.detached, values)
		return
	}
	held.overflow += exec.reserveLoopScratch(newMemoryEstimator().slice(values))
}

// holdArrayBackings claims the element backings a frame is about to hold across
// the script it runs, and returns the mark releaseArrayBackings takes to drop
// them again.
//
// A frame's receiver is the obvious one, but not the only one: a global builtin
// and a capability method are dispatched with no receiver at all and drive their
// block from an argument instead. Keyword arguments reach a frame the same way.
// hostDriven frames take a wildcard claim covering all of it and more, since
// what their Go body captured is not knowable from the call.
//
// The claims are taken where the driver captures the headers it will walk --
// builtin dispatch for the member functions and adapters, the loop head for
// `for x in a` -- so nothing has run in between that could have moved a value
// onto a different backing.
func (exec *Execution) holdArrayBackings(receiver Value, args []Value, kwargs map[string]Value, hostDriven bool) int {
	mark := len(exec.heldArrayBackings)
	if hostDriven {
		exec.heldArrayBackings = append(exec.heldArrayBackings,
			heldArrayBacking{depth: exec.builtinDepth, wildcard: true})
		return mark
	}
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

// releaseArrayBackings drops every claim taken since mark. Dropping a claim
// that had detached headers also bumps the mutation epoch: they stop being live
// state at that moment, and without the bump the base-walk memo would keep
// serving a total that still includes them.
//
// The released claims are cleared rather than only cut off. A claim carries the
// headers it walks, so shortening the stack would leave those pointers in its
// backing, keeping the arrays reachable from the execution while a walk that
// reads only the live claims stops counting them -- the very shape this file
// exists to fix. Running one iteration over a 48 MiB array and then dropping it
// held all of it.
func (exec *Execution) releaseArrayBackings(mark int) error {
	if mark >= len(exec.heldArrayBackings) {
		return nil
	}
	released := exec.heldArrayBackings[mark:]
	bumped := false
	var reclaimErr error
	for _, held := range released {
		exec.releaseLoopScratch(held.overflow)
		for _, retained := range held.retained {
			// Moving the array off its storage is safe whoever else is still
			// walking that storage, so a claim below this one needs no say in
			// it and nothing has to be handed on to one.
			if err := retained.reclaim(exec); err != nil && reclaimErr == nil {
				reclaimErr = err
			}
		}
		if !bumped && (len(held.detached) > 0 || len(held.retained) > 0 || held.overflow > 0) {
			bumpMutationEpoch()
			bumped = true
		}
	}
	clear(released)
	exec.heldArrayBackings = exec.heldArrayBackings[:mark]
	return reclaimErr
}

// canRetain reports whether this claim can take on full without exceeding the
// header cap. A backing it already holds costs nothing more, which is the case a
// drain hits: it narrows the same storage over and over.
func (held *heldArrayBacking) canRetain(full []Value) bool {
	return len(held.retained) < maxHeldArrayHeaders || held.holds(full)
}

func (held *heldArrayBacking) holds(full []Value) bool {
	id := sliceBackingIdentity(full)
	for _, already := range held.retained {
		if sliceBackingIdentity(already.full) == id {
			return true
		}
	}
	return false
}

// retain records an array a shrink narrowed without touching its storage, so
// the whole header keeps costing while this claim is live.
func (held *heldArrayBacking) retain(full []Value, receiver Value) {
	if held.holds(full) {
		return
	}
	held.retained = append(held.retained, retainedArrayBacking{full: full, receiver: receiver})
}

// reclaim moves the array off the storage it has been narrowing, once the frame
// that may have been walking that storage is done. It is the deferred half of
// the shrink: what the array gave up has to stop being reachable through it, and
// this is the first moment anything can be done about it.
//
// It moves rather than clears. Clearing would compute which slots to blank from
// this one array's window, and a Go slice can be behind more than one array: a
// host that builds two values over one slice leaves the second showing elements
// the first has given up, and blanking them by the first array's window empties
// the second. Copying the survivors out touches nothing, so whatever else is
// behind that storage keeps reading exactly what it read before, and the storage
// itself goes when the last of them does.
//
// The array may have moved on already -- onto a backing a copy under the cap
// gave it, or one a growing push allocated -- in which case there is nothing
// left to move it off.
func (retained retainedArrayBacking) reclaim(exec *Execution) error {
	current := retained.receiver.Array()
	if sliceBackingIdentity(current) == 0 || !sliceWithin(retained.full, current) {
		return nil
	}
	if err := exec.chargeScanSteps(len(current)); err != nil {
		return err
	}
	acc := newArrayBuildAccumulator(exec, retained.receiver, nil, nil, NewNil())
	if err := acc.reserveSlotArrays(len(current)); err != nil {
		return err
	}
	moved := make([]Value, len(current))
	copy(moved, current)
	setArrayElems(retained.receiver, moved)
	return nil
}

// sliceWithin reports whether inner is a window onto outer's storage.
func sliceWithin(outer, inner []Value) bool {
	head := cap(outer) - cap(inner)
	if head < 0 || head > len(outer) || len(inner) > len(outer)-head {
		return false
	}
	return sliceBackingIdentity(outer[head:]) == sliceBackingIdentity(inner)
}

// retainedArrayBackingBytes is the memory of every header a shrink narrowed in
// place beneath a wildcard claim. The array itself is charged for the window it
// still shows; this adds back what it vacated but has not yet released.
//
// The elements are walked one by one rather than through the slice accounting,
// which deduplicates on the backing: the array still points into that same
// backing, so it has already been counted and the payloads hanging off the
// vacated slots would come back as nothing. Walking the elements charges those
// payloads while still deduplicating each one against the rest of the graph, at
// the cost of counting the fixed per-value bytes of the visible window twice.
func (exec *Execution) retainedArrayBackingBytes(est *memoryEstimator) int {
	total := 0
	for _, held := range exec.heldArrayBackings {
		for _, retained := range held.retained {
			for _, val := range retained.full {
				total += est.value(val)
			}
		}
	}
	return total
}

// detachedArrayBackingBytes is the memory of every header a shrink has copied
// away from while its holder is still walking it.
//
// The holder keeps the header alive on its Go stack, but the receiver no longer
// points at it, so no root the estimator walks reaches it any more. A block that
// drained its receiver and dropped the result could then allocate a second
// quota's worth against a receiver the shrink had just emptied, with the first
// generation still live and uncounted.
func (exec *Execution) detachedArrayBackingBytes(est *memoryEstimator) int {
	total := 0
	for _, held := range exec.heldArrayBackings {
		for _, elems := range held.detached {
			total += est.slice(elems)
		}
	}
	return total
}

// wildcardArrayClaim returns the enclosing wildcard claim that may be walking
// values' backing, or nil when none is.
func (exec *Execution) wildcardArrayClaim(values []Value) *heldArrayBacking {
	if exec == nil || len(exec.heldArrayBackings) == 0 {
		return nil
	}
	if sliceBackingIdentity(values) == 0 {
		return nil
	}
	for i := range exec.heldArrayBackings {
		held := &exec.heldArrayBackings[i]
		if held.wildcard && held.depth < exec.builtinDepth {
			return held
		}
	}
	return nil
}

// namedArrayBackingHeld reports whether an enclosing frame the runtime wrote
// named values' backing.
func (exec *Execution) namedArrayBackingHeld(values []Value) bool {
	if exec == nil || len(exec.heldArrayBackings) == 0 {
		return false
	}
	id := sliceBackingIdentity(values)
	if id == 0 {
		return false
	}
	for _, held := range exec.heldArrayBackings {
		if !held.wildcard && held.id == id && held.depth < exec.builtinDepth {
			return true
		}
	}
	return false
}

// detachArrayBackingClaims reports whether a frame enclosing the running one may
// be walking values' backing, and so may still read slots this frame is about to
// vacate. The running frame's own claim, recorded by its dispatch, sits at the
// current builtin depth and is not an enclosing one.
//
// Every enclosing claim it finds takes the header, because the shrink that asked
// is about to move the receiver onto a fresh backing and leave those holders as
// the only thing keeping this one alive.
func (exec *Execution) detachArrayBackingClaims(values []Value) bool {
	if exec == nil || len(exec.heldArrayBackings) == 0 {
		return false
	}
	id := sliceBackingIdentity(values)
	if id == 0 {
		return false
	}
	found := false
	for i := range exec.heldArrayBackings {
		held := &exec.heldArrayBackings[i]
		if held.depth >= exec.builtinDepth || !held.claims(id) {
			continue
		}
		held.recordDetached(exec, values)
		found = true
	}
	return found
}

// shrinkArray narrows the receiver to arr[start:end] in place, leaving the
// elements outside that window unreachable from it. pendingSlots is the size of
// any slot array the caller has already allocated but not yet published (the
// removed elements pop(n) and shift(n) return), so a copy is priced against the
// whole live peak rather than against the receiver alone.
func shrinkArray(exec *Execution, receiver Value, arr []Value, start, end int,
	args []Value, kwargs map[string]Value, block Value, pendingSlots int,
) error {
	window := arr[start:end]
	named := exec.namedArrayBackingHeld(arr)
	wildcard := exec.wildcardArrayClaim(arr)
	if !named && wildcard == nil {
		clear(arr[:start])
		clear(arr[end:])
		setArrayElems(receiver, window)
		return nil
	}
	if !named && wildcard.canRetain(arr) {
		// A frame the runtime did not write may be walking this header, and
		// may also be handed whatever the shrink moves the array onto, so the
		// storage is left exactly as it is and the array just shows less of
		// it. The claim charges the whole header until it drops, and clears
		// the vacated slots then. This is the cheaper half of the shrink as
		// well as the safe one: a drain narrows the same storage over and
		// over and never copies.
		wildcard.retain(arr, receiver)
		setArrayElems(receiver, window)
		return nil
	}
	// A frame the runtime wrote is walking this header, or a wildcard claim is
	// already holding as many as it accounts for. Copying the survivors out is
	// what leaves the header alone, and the header the frame is left holding
	// goes on the quota.
	if err := exec.chargeScanSteps(len(window)); err != nil {
		return err
	}
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	if err := acc.reserveSlotArrays(len(window), pendingSlots); err != nil {
		return err
	}
	exec.detachArrayBackingClaims(arr)
	fresh := make([]Value, len(window))
	copy(fresh, window)
	setArrayElems(receiver, fresh)
	return nil
}
