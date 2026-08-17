package runtime

import (
	"maps"
	"os"
	"slices"

	"github.com/mgomes/vibescript/vibes/value"
)

// Arrays and hashes are values (ADR-006 item 2). Binding, passing, or returning
// one produces another logical value, and updating one binding or path can
// never change a sibling. The runtime implements that with copy-on-write:
// naming a collection is free, and the copy is paid only where a write meets a
// wrapper something else can still see.
//
// Two facts carry the whole scheme.
//
// The first is the ref state on each wrapper (see vibes/value/collection_refs.go),
// a saturating count of the durable slots naming it. publishCollection raises it
// wherever a collection is placed somewhere that outlives the expression that
// produced it. Because it saturates and never decrements, it can over-count --
// costing a copy that was not strictly needed -- but never under-count, which
// would lose an alias.
//
// The second is addressability. A write may only mutate in place when it can
// name the one slot it is updating: a local, an instance or class variable, or a
// path of index and hash-member steps rooted at one of those. resolveMutablePath
// walks that path, copying and rebinding each level that is shared, so the leaf
// it returns is a wrapper only this path can reach. A receiver that is not
// addressable -- a call result, a literal, an instance accessor -- is a
// temporary: it is copied before any write, so the update is returned but
// changes nothing else, which is exactly what the ADR requires.
//
// Nothing about this is script-visible. Sharing a wrapper is a representation
// detail the memory estimator may still measure; the language sees only values.

// alwaysCopyCollections makes an addressable write copy and rebind its whole
// path unconditionally, instead of asking whether each level is exclusively
// held. Copy-per-write is the reference semantics the ref state optimizes, so
// running a suite with it on and off must produce identical results: a write
// that mutated in place because refs under-counted is exactly the one the oracle
// copies instead, and the two runs then disagree.
//
// It is the only oracle for this design's one bug class -- a store that creates
// a durable handle without publishing it -- because such a store is invisible
// until some later write happens to reach the wrapper it left uncounted. It is
// read once, in the runtime package's test TestMain, and is never on in
// production.
var alwaysCopyCollections = false

// maybeEnableCollectionCopyVerify turns on the always-copy oracle when
// VIBES_COW_ALWAYS_COPY is set. It is called from TestMain before any test
// runs, so the flag is write-once.
func maybeEnableCollectionCopyVerify() {
	if os.Getenv("VIBES_COW_ALWAYS_COPY") != "" {
		alwaysCopyCollections = true
	}
}

// isCollection reports whether val carries a wrapper that value semantics
// governs. Objects are included: a host attribute bag is script-mutable through
// the same index and member writes a hash is, so it owes the same isolation.
func isCollection(val Value) bool {
	switch val.Kind() {
	case KindArray, KindHash, KindObject:
		return true
	default:
		return false
	}
}

// publishCollection records that another durable slot now names val. Every site
// that stores a value somewhere outliving the current expression calls it: the
// environment's terminal writes, instance and class variable stores, hash entry
// stores, and the array builders that adopt elements they did not construct.
//
// It is deliberately cheap and total -- a kind check and at most one store --
// so that calling it at a site that turns out not to need it costs nothing but
// a copy that a later write might not have made, while omitting it at a site
// that does need it loses an alias.
func publishCollection(val Value) {
	if isCollection(val) {
		val.PublishRef()
	}
}

// publishBindingReplacement counts the handle a store into an already-occupied
// slot creates, unless the slot already names the same wrapper. Writing a
// mutator's result back over the receiver it updated -- `items = items.push(x)`,
// `@rows = @rows.delete_if { ... }` -- duplicates nothing, and counting it would
// leave the binding looking shared and make the next write copy.
func publishBindingReplacement(previous, next Value) {
	if isCollection(next) {
		value.PublishReplacement(previous, next)
	}
}

// publishCollectionElems publishes every collection among elems. The mutators
// that graft values into an existing array call it: the array's element slots
// now name them as well as wherever the arguments came from. Array construction
// publishes through NewArray instead, which sees every element at once.
func publishCollectionElems(elems []Value) {
	value.PublishRefElems(elems)
}

// writeArrayElems installs elems as the elements of the array a mutator is
// updating, checking writability at the write rather than trusting a check made
// earlier in the call. It returns the wrapper actually written, which is the
// value the mutator returns.
//
// Every in-place array mutator ends here, which is what makes the check
// structural: a mutator that runs script code between its first check and its
// write -- a block, an argument expression -- cannot forget to look again,
// because looking again is what writing is.
func (exec *Execution) writeArrayElems(receiver Value, elems []Value) (Value, error) {
	target, err := exec.writableCollection(receiver)
	if err != nil {
		return NewNil(), err
	}
	setArrayElems(target, elems)
	return target, nil
}

// writeHashClear empties the hash a mutator is updating, checking writability at
// the write for the same reason writeArrayElems does.
func (exec *Execution) writeHashClear(receiver Value) (Value, error) {
	target, err := exec.writableCollection(receiver)
	if err != nil {
		return NewNil(), err
	}
	hashClearEntries(target)
	return target, nil
}

// collectionIdentity returns the wrapper identity of a collection, or 0 for
// anything else. It identifies the wrapper rather than its storage, so it
// survives an in-place growth that reallocates the backing.
func collectionIdentity(val Value) uintptr {
	switch val.Kind() {
	case KindArray:
		return arrayIdentity(val)
	case KindHash:
		return hashIdentity(val)
	case KindObject:
		return value.ObjectIdentity(val)
	default:
		return 0
	}
}

// copyCollection returns an independent wrapper over the same contents, charged
// against the memory quota before it allocates and billed to the step quota for
// the elements it walks. Nested collections are not copied: they are published
// instead, so each stays one value until something writes through it, at which
// point that write makes its own copy along its own path.
//
// The copy is the whole cost of value semantics, and it is why binding is free.
// A caller that already knows the wrapper is exclusively held must not call it.
func (exec *Execution) copyCollection(val Value) (Value, error) {
	switch val.Kind() {
	case KindArray:
		elems := val.Array()
		if err := exec.checkSlotReservationWithCallRoots(len(elems), val, nil, nil, NewNil()); err != nil {
			return NewNil(), err
		}
		if err := exec.chargeScanSteps(len(elems)); err != nil {
			return NewNil(), err
		}
		copied := slices.Clone(elems)
		if copied == nil {
			copied = []Value{}
		}
		return NewArray(copied), nil
	case KindHash:
		entries := val.Hash()
		if err := exec.checkProjectedHashBytes(len(entries), val, nil, nil, NewNil()); err != nil {
			return NewNil(), err
		}
		if err := exec.chargeScanSteps(len(entries)); err != nil {
			return NewNil(), err
		}
		copied := maps.Clone(entries)
		if copied == nil {
			copied = map[string]Value{}
		}
		return value.NewHashWithOrder(copied, val.HashKeyOrder()), nil
	case KindObject:
		entries := val.Hash()
		if err := exec.checkProjectedHashBytes(len(entries), val, nil, nil, NewNil()); err != nil {
			return NewNil(), err
		}
		if err := exec.chargeScanSteps(len(entries)); err != nil {
			return NewNil(), err
		}
		copied := maps.Clone(entries)
		if copied == nil {
			copied = map[string]Value{}
		}
		// A tagged bag renders a string form fixed at construction and refuses
		// writes, so a copy of one is only ever made on a path that already
		// rejected the write. Rebuilding it as an ordinary bag would launder
		// the provenance, so the tag rides along.
		if tag := val.ObjectTag(); tag != ObjectTagNone {
			form, _ := val.ObjectStringForm()
			return value.NewTaggedObject(copied, tag, form), nil
		}
		return NewObject(copied), nil
	default:
		return val, nil
	}
}

// writableCollection returns a wrapper the caller may write through. It is the
// guard every in-place mutator passes, so that no dispatch route -- a member
// call, send, an operator, a host builtin -- can reach a write with a receiver
// something else can still see.
//
// A wrapper no durable slot names yet is always writable. Beyond that the caller
// must have reached it through the path that owns it, which resolveMutablePath
// records on the execution for the duration of one call; without that record a
// receiver that lives in a slot is a temporary, and the write goes to a copy
// nothing else can see.
//
// The always-copy oracle deliberately does not reach here. It forces the path
// walk to copy, which installs a fresh wrapper at the slot being written; making
// this copy a second time would leave the write in a wrapper installed nowhere
// and lose it, which is a bug in the oracle rather than a finding.
func (exec *Execution) writableCollection(val Value) (Value, error) {
	if !isCollection(val) {
		return val, nil
	}
	if val.Unpublished() {
		return val, nil
	}
	if val.SoleRef() && exec.addressed.leaf != 0 && exec.addressed.leaf == collectionIdentity(val) {
		return val, nil
	}
	// The receiver was resolved as an addressable path, and script code has
	// published it since -- an argument expression, this mutator's own block, a
	// right side. Isolating again through the recorded path both restores
	// exclusive ownership and reinstalls the result where the source names it,
	// which a bare copy would not: the write would land somewhere nothing can
	// reach and the update would simply be lost.
	if exec.addressed.valid && exec.addressed.leaf == collectionIdentity(val) {
		leaf, chain, err := exec.isolateMutablePath(exec.addressed.target, exec.addressed.env)
		if err != nil {
			return NewNil(), err
		}
		if isCollection(leaf) {
			exec.addressed.leaf = collectionIdentity(leaf)
			exec.addressed.path = chain
			return leaf, nil
		}
	}
	return exec.copyCollection(val)
}

// addressCollection records that val was reached through the path that owns it,
// so the write about to run may proceed in place. The returned restore function
// must run before control leaves the call, which is what keeps the record from
// vouching for a later expression that reached the same wrapper another way.
func (exec *Execution) addressCollection(val Value, chain []uintptr, target mutablePath, env *Env) {
	exec.addressed = addressedScope{
		leaf:   collectionIdentity(val),
		path:   chain,
		target: target,
		env:    env,
		valid:  true,
	}
}

// savedAddressedScope snapshots the permission currently in force. Callers take
// it before resolving a receiver and defer restoring it, which withdraws the
// permission on every exit without a closure to allocate.
func (exec *Execution) savedAddressedScope() addressedScope {
	return exec.addressed
}

// addressedScope is the permission state a write displaced, so that restoring it
// is a plain struct copy passed to a deferred call. A closure here allocated on
// every mutating write, which the shovel-in-a-loop benchmarks saw.
//
// It carries the resolved path, not just the leaf, because the permission has to
// survive script code running between the moment it was granted and the moment
// the write lands -- an argument expression, a mutator's block, a compound
// assignment's right side. Any of those can bind the receiver somewhere new, and
// the write then has to isolate again. Holding the path is what lets the write
// do that itself instead of trusting that nothing happened.
type addressedScope struct {
	leaf   uintptr
	path   []uintptr
	target mutablePath
	env    *Env
	valid  bool
}

// restore withdraws the permission addressCollection granted.
func (exec *Execution) restore(saved addressedScope) {
	exec.addressed = saved
}

// detachStoredCollection returns the value a write should place in a slot of the
// collection it is updating, copying it when it is the receiver itself or one of
// the containers the write reached the receiver through.
//
// Storing a container into itself is the one way collections could still form a
// cycle, and under value semantics it must not: `list.push(list)` appends the
// list as it was, not a window onto the list as it is about to become. Copying
// the value is what makes those two the same thing, and it leaves collections
// acyclic by construction -- a graph the estimator, equality, and rendering no
// longer have to defend against for arrays and hashes.
//
// A container reached some other way needs no copy, because reaching it that way
// published it, and the write that isolated the receiver already copied
// everything the two had in common.
func (exec *Execution) detachStoredCollection(val Value) (Value, error) {
	if !isCollection(val) || exec.addressed.leaf == 0 {
		return val, nil
	}
	id := collectionIdentity(val)
	if id == 0 {
		return val, nil
	}
	if id != exec.addressed.leaf && !slices.Contains(exec.addressed.path, id) {
		return val, nil
	}
	// The copy must be independent of the container being written, and a
	// shallow one is not: copying `a` for `a[0].push(a)` produces a second
	// wrapper that still holds the very element the push is about to write
	// into, which is the cycle this exists to prevent. A deep copy is also the
	// right answer rather than a workaround -- what the write stores is the
	// value the container had, all the way down, not a window onto the value it
	// is about to have.
	detached := deepCloneValue(val)
	if err := exec.checkMemoryValue(detached); err != nil {
		return NewNil(), err
	}
	return detached, nil
}

// detachStoredCollections applies detachStoredCollection across a mutator's
// arguments, returning the original slice untouched when none of them names a
// container on the write's own path.
func (exec *Execution) detachStoredCollections(vals []Value) ([]Value, error) {
	if exec.addressed.leaf == 0 {
		return vals, nil
	}
	var detached []Value
	for i, val := range vals {
		replacement, err := exec.detachStoredCollection(val)
		if err != nil {
			return nil, err
		}
		if collectionIdentity(replacement) == collectionIdentity(val) {
			continue
		}
		if detached == nil {
			detached = slices.Clone(vals)
		}
		detached[i] = replacement
	}
	if detached == nil {
		return vals, nil
	}
	return detached, nil
}

// mutableRootKind names where a mutable path begins.
type mutableRootKind uint8

const (
	// mutableRootNone marks an expression that names no slot the runtime can
	// rebind, so a write through it updates a temporary.
	mutableRootNone mutableRootKind = iota
	mutableRootLocal
	mutableRootIvar
	mutableRootClassVar
)

// mutableRoot is the rebindable slot a mutable path starts at.
type mutableRoot struct {
	kind mutableRootKind
	name string
	env  *Env
	vars map[string]Value
}

func (root mutableRoot) get() (Value, bool) {
	switch root.kind {
	case mutableRootLocal:
		return root.env.Get(root.name)
	case mutableRootIvar, mutableRootClassVar:
		val, ok := root.vars[root.name]
		return val, ok
	default:
		return NewNil(), false
	}
}

// rebind installs a copy at the slot the path starts at. It never publishes:
// the copy replaces the slot's previous occupant, so the slot count does not
// grow, and publishing here would make the next write through the same slot
// copy again -- turning a mutating loop quadratic.
func (root mutableRoot) rebind(val Value) {
	switch root.kind {
	case mutableRootLocal:
		root.env.Assign(root.name, val)
	case mutableRootIvar, mutableRootClassVar:
		bumpMutationEpoch()
		root.vars[root.name] = val
	}
	val.AdoptSoleRef()
}

// mutablePathStep is one index or hash-member hop of a mutable path.
//
// An index hop keeps its source expression because the index is evaluated once,
// on the way down, and reused for the write-back: re-evaluating it could run
// script code twice and address a different slot the second time.
type mutablePathStep struct {
	expr    *IndexExpr
	member  *MemberExpr
	index   Value
	indexed bool
	pos     Position
}

// mutablePath is an addressable receiver: a rebindable root plus the hops that
// reach the value being written.
type mutablePath struct {
	root  mutableRoot
	steps []mutablePathStep
}

// resolveMutableReceiver evaluates expr as the receiver of an in-place write,
// reporting false when expr names no addressable path -- the caller then
// evaluates it however it ordinarily would, and the write goes to a temporary.
//
// When expr does name one, each level of that path is made exclusively held on
// the way down -- copied and rebound where it is shared -- so the leaf is a
// wrapper only this path reaches, and the write lands where the script can see
// it. The caller must have taken savedAddressedScope and deferred restoring it.
func (exec *Execution) resolveMutableReceiver(expr Expression, env *Env) (Value, bool, error) {
	path, ok := exec.mutablePathFor(expr, env)
	if !ok {
		return NewNil(), false, nil
	}
	leaf, chain, addressable, resolved, err := exec.walkMutablePath(path, env)
	if err != nil {
		return NewNil(), true, err
	}
	if !resolved {
		// The walk read nothing the caller can use -- a bare name bound to a
		// function is invoked where a receiver is expected, so what the slot
		// holds is not what the expression denotes. Ordinary evaluation owns it.
		return NewNil(), false, nil
	}
	// A path that left collection storage -- a member of a class instance, an
	// index into a string -- read the value the ordinary evaluation would have,
	// so the caller keeps it, but it names no slot the write may update. Leaving
	// the permission ungranted is what makes that write land on a copy.
	if addressable {
		exec.addressCollection(leaf, chain, path, env)
	}
	return leaf, true, nil
}

// rootAutoInvokes reports whether reading this root the ordinary way would call
// it rather than yield it. A bare name bound to a function or builtin is invoked
// where a receiver is expected, so the value a path walk would find there is not
// the one the expression denotes; such a root is left to ordinary evaluation.
func rootAutoInvokes(val Value) bool {
	switch val.Kind() {
	case KindFunction, KindBuiltin:
		return true
	default:
		return false
	}
}

// resolveMutableTarget is resolveMutableReceiver for a write whose value is only
// computed after the target is prepared -- a compound assignment evaluates
// `a[0] += f()` by reading a[0], running f, and writing back. It hands the path
// back so the write can isolate again immediately before it lands, and it leaves
// the permission it granted withdrawn, because evaluating the right side runs
// script code that must not inherit it.
//
// Isolating twice is not redundant. The right side can bind the receiver
// somewhere new (`a[0] += (b = a; 1)`), so a wrapper that was exclusively held
// when the target was prepared need not still be when the write happens. The
// second pass costs a re-read of an already-resolved path, whose keys are cached
// and whose reads are pure, so nothing is evaluated or invoked twice.
func (exec *Execution) resolveMutableTarget(expr Expression, env *Env) (Value, mutablePath, bool, error) {
	saved := exec.savedAddressedScope()
	defer exec.restore(saved)

	path, ok := exec.mutablePathFor(expr, env)
	if !ok {
		return NewNil(), mutablePath{}, false, nil
	}
	leaf, _, addressable, resolved, err := exec.walkMutablePath(path, env)
	if err != nil || !resolved {
		return NewNil(), mutablePath{}, false, err
	}
	if !addressable {
		return leaf, mutablePath{}, false, nil
	}
	return leaf, path, true, nil
}

// writeThroughMutablePath isolates the path again and runs write against the
// leaf that isolation produced, with the addressability record in force for
// exactly that write.
func (exec *Execution) writeThroughMutablePath(path mutablePath, env *Env, write func(Value) error) error {
	leaf, chain, err := exec.isolateMutablePath(path, env)
	if err != nil {
		return err
	}
	saved := exec.savedAddressedScope()
	exec.addressCollection(leaf, chain, path, env)
	defer exec.restore(saved)
	return write(leaf)
}

// mutablePathFor decomposes expr into a rebindable root and the hops from it,
// reporting false for anything that names no such slot.
//
// Index and hash-member hops qualify because the runtime can write the hop back:
// an array slot and a hash entry are storage it owns. A member hop onto a class
// instance does not, because reading it may run an accessor and writing it may
// run a setter, so the runtime cannot claim the read and the write address one
// slot. Inside the class the same state is addressable as an instance variable.
func (exec *Execution) mutablePathFor(expr Expression, env *Env) (mutablePath, bool) {
	var steps []mutablePathStep
	for {
		switch t := expr.(type) {
		case *Identifier:
			if isClassConstantAssignmentName(t.Name, env) {
				return mutablePath{}, false
			}
			// Whether the name is bound is left to the root read below: asking
			// here would look the binding up twice on every write.
			return mutablePath{
				root:  mutableRoot{kind: mutableRootLocal, name: t.Name, env: env},
				steps: reverseSteps(steps),
			}, true
		case *IvarExpr:
			self, ok := env.Get("self")
			if !ok || self.Kind() != KindInstance {
				return mutablePath{}, false
			}
			return mutablePath{
				root:  mutableRoot{kind: mutableRootIvar, name: t.Name, vars: valueInstance(self).Ivars},
				steps: reverseSteps(steps),
			}, true
		case *ClassVarExpr:
			self, ok := env.Get("self")
			if !ok {
				return mutablePath{}, false
			}
			var vars map[string]Value
			switch self.Kind() {
			case KindInstance:
				vars = valueInstance(self).Class.ClassVars
			case KindClass:
				vars = valueClass(self).ClassVars
			default:
				return mutablePath{}, false
			}
			return mutablePath{
				root:  mutableRoot{kind: mutableRootClassVar, name: t.Name, vars: vars},
				steps: reverseSteps(steps),
			}, true
		case *IndexExpr:
			if len(t.Indices) != 1 {
				return mutablePath{}, false
			}
			steps = append(steps, mutablePathStep{expr: t, pos: t.Position})
			expr = t.Object
		case *MemberExpr:
			steps = append(steps, mutablePathStep{member: t, pos: t.Pos()})
			expr = t.Object
		default:
			return mutablePath{}, false
		}
	}
}

func reverseSteps(steps []mutablePathStep) []mutablePathStep {
	slices.Reverse(steps)
	return steps
}

// walkMutablePath resolves the value the caller is about to write through, in
// two passes.
//
// The first reads the path, evaluating each index exactly once and recording
// whether every hop stayed inside collection storage. A hop that leaves it -- a
// member of a class instance, an index into a string -- is read the way an
// ordinary expression reads it, and from that point the path addresses no slot
// the runtime can rebind, so what it produces is a temporary.
//
// The second pass runs only for a fully addressable path whose leaf is a
// collection, and makes each level exclusively held on the way down. Its reads
// are pure -- an array index and a hash lookup with the key the first pass
// already resolved -- so nothing is evaluated or invoked twice.
//
// Copying a level publishes the collections it holds, so a level below a copied
// one is shared by construction and is copied in turn. That is not incidental:
// after the copy both wrappers name the same child, and only one of them may go
// on writing through it.
func (exec *Execution) walkMutablePath(path mutablePath, env *Env) (Value, []uintptr, bool, bool, error) {
	if len(path.steps) == 0 {
		// A bare local, instance variable, or class variable is the common
		// case -- `a.push(x)`, `@rows << row` -- and needs neither the reading
		// pass nor an ancestor list, so it reads the slot once and isolates it
		// on the spot.
		current, ok := path.root.get()
		if !ok || rootAutoInvokes(current) {
			return NewNil(), nil, false, false, nil
		}
		if !isCollection(current) || exec.exclusivelyHeld(current) {
			return current, nil, true, true, nil
		}
		copied, err := exec.copyCollection(current)
		if err != nil {
			return NewNil(), nil, false, true, err
		}
		path.root.rebind(copied)
		return copied, nil, true, true, nil
	}
	leaf, addressable, resolved, err := exec.readMutablePath(path, env)
	if err != nil || !resolved {
		return NewNil(), nil, false, resolved, err
	}
	if !addressable || !isCollection(leaf) {
		return leaf, nil, false, true, nil
	}
	isolated, chain, err := exec.isolateMutablePath(path, env)
	return isolated, chain, err == nil, true, err
}

// readMutablePath is the reading pass. It reports whether every hop addressed
// collection storage, which is what makes the isolating pass safe to run, and
// whether it resolved anything at all -- a root that would be invoked rather
// than read belongs to ordinary evaluation.
func (exec *Execution) readMutablePath(path mutablePath, env *Env) (Value, bool, bool, error) {
	current, ok := path.root.get()
	if !ok || rootAutoInvokes(current) {
		return NewNil(), false, false, nil
	}
	for i := range path.steps {
		step := &path.steps[i]
		if !isCollection(current) {
			tail, err := exec.readPathTailOrdinarily(current, path.steps[i:], env)
			return tail, false, true, err
		}
		child, found, err := exec.readCollectionStep(current, step, env)
		if err != nil {
			return NewNil(), false, true, err
		}
		if !found {
			return NewNil(), true, true, nil
		}
		current = child
	}
	return current, true, true, nil
}

// isolateMutablePath is the isolating pass: it descends an addressable path
// copying and rebinding every level a second slot can still reach.
func (exec *Execution) isolateMutablePath(path mutablePath, env *Env) (Value, []uintptr, error) {
	current, ok := path.root.get()
	if !ok {
		return NewNil(), nil, nil
	}
	if isCollection(current) && !exec.exclusivelyHeld(current) {
		copied, err := exec.copyCollection(current)
		if err != nil {
			return NewNil(), nil, err
		}
		path.root.rebind(copied)
		current = copied
	}
	// The chain lists the containers above the leaf, which the leaf's own
	// identity on the execution completes. Only a path with steps has any, so
	// the common bare-root write allocates nothing.
	chain := make([]uintptr, 0, len(path.steps))
	for i := range path.steps {
		chain = append(chain, collectionIdentity(current))
		step := &path.steps[i]
		child, found, err := exec.readCollectionStep(current, step, env)
		if err != nil || !found {
			return NewNil(), nil, err
		}
		if isCollection(child) && !exec.exclusivelyHeld(child) {
			copied, err := exec.copyCollection(child)
			if err != nil {
				return NewNil(), nil, err
			}
			if err := exec.storeMutableStep(current, step, copied); err != nil {
				return NewNil(), nil, err
			}
			copied.AdoptSoleRef()
			child = copied
		}
		current = child
	}
	return current, chain, nil
}

// exclusivelyHeld reports whether a collection reached through the path that
// owns it may be written in place. The always-copy oracle answers no for every
// collection, which turns the whole scheme back into a copy per write.
func (exec *Execution) exclusivelyHeld(val Value) bool {
	return !alwaysCopyCollections && val.SoleRef()
}

// readPathTailOrdinarily finishes a path that has left collection storage,
// reading each remaining hop exactly as evaluating the expression would. It runs
// once, in the reading pass, so a getter it invokes runs once.
func (exec *Execution) readPathTailOrdinarily(current Value, steps []mutablePathStep, env *Env) (Value, error) {
	for i := range steps {
		step := &steps[i]
		if step.member != nil {
			if step.member.Safe && current.Kind() == KindNil {
				return NewNil(), nil
			}
			member, err := exec.getPublicMember(current, step.member.Property, step.member.Pos())
			if err != nil {
				return NewNil(), err
			}
			current, err = exec.autoInvokeIfNeeded(step.member, member, current)
			if err != nil {
				return NewNil(), err
			}
			continue
		}
		indices, err := exec.evalIndexSelectors(step.expr, current, env)
		if err != nil {
			return NewNil(), err
		}
		current, err = exec.evalIndexValue(step.expr, current, indices)
		if err != nil {
			return NewNil(), err
		}
	}
	return current, nil
}

// readCollectionStep reads one hop that addresses collection storage, caching
// the hop's key the first time so the isolating pass and the write-back address
// the same slot the read did.
func (exec *Execution) readCollectionStep(container Value, step *mutablePathStep, env *Env) (Value, bool, error) {
	if step.member != nil {
		switch container.Kind() {
		case KindHash, KindObject:
			key := NewString(step.member.Property)
			if container.Kind() == KindHash {
				key = hashMemberAssignmentKey(container, step.member.Property)
			}
			step.index = key
			val, ok, err := hashGet(container, key)
			if err != nil {
				return NewNil(), false, exec.errorAt(step.pos, "%s", err.Error())
			}
			return val, ok, nil
		default:
			return NewNil(), false, nil
		}
	}
	if !step.indexed {
		indices, err := exec.evalIndexSelectors(step.expr, container, env)
		if err != nil {
			return NewNil(), false, err
		}
		if len(indices) != 1 {
			return NewNil(), false, nil
		}
		step.index = indices[0]
		step.indexed = true
	}
	switch container.Kind() {
	case KindArray:
		i, err := exec.indexSelectorToInt(step.expr, step.index, 0)
		if err != nil {
			return NewNil(), false, err
		}
		elems := container.Array()
		if i < 0 {
			i += len(elems)
		}
		if i < 0 || i >= len(elems) {
			return NewNil(), false, nil
		}
		return elems[i], true, nil
	case KindHash, KindObject:
		val, ok, err := hashGet(container, step.index)
		if err != nil {
			return NewNil(), false, exec.errorAt(step.pos, "%s", err.Error())
		}
		return val, ok, nil
	default:
		return NewNil(), false, nil
	}
}

// storeMutableStep writes a copied level back into its exclusively held parent.
// It does not publish: the copy replaces what the slot already held.
func (exec *Execution) storeMutableStep(container Value, step *mutablePathStep, val Value) error {
	switch container.Kind() {
	case KindArray:
		i, err := exec.indexSelectorToInt(step.expr, step.index, 0)
		if err != nil {
			return err
		}
		elems := container.Array()
		if i < 0 {
			i += len(elems)
		}
		if i < 0 || i >= len(elems) {
			return nil
		}
		bumpMutationEpoch()
		elems[i] = val
		return nil
	case KindHash, KindObject:
		if err := container.HashSetOwned(step.index, val); err != nil {
			return exec.errorAt(step.pos, "%s", err.Error())
		}
		return nil
	default:
		return nil
	}
}

// isCollectionMutator reports whether property names a member that updates its
// receiver in place. Dispatch consults it before evaluating the receiver, so
// that a mutator gets an addressable path where the source provides one, and the
// mutators themselves consult writableCollection so that no other dispatch route
// -- send, an operator, a host builtin -- can reach a write past the guard.
//
// The bang-named members that duplicated a non-bang transformation are gone
// (ADR-006 item 2), so what is left is the set with no non-mutating spelling:
// growth, removal, and whole-receiver replacement.
//
// It is a switch rather than a set lookup because every member expression and
// every member call asks it, and hashing the property name there was
// measurable. The names are also strings a String receiver answers
// non-mutatingly (delete, replace, insert, clear, prepend), which costs those
// calls the reading pass of a path walk and nothing else: the walk isolates
// nothing once it sees the receiver is not a collection.
func isCollectionMutator(property string) bool {
	switch property {
	case "push", "append", "prepend", "unshift", "pop", "shift", "insert", "fill",
		"delete", "delete_if", "keep_if", "clear", "store", "replace":
		return true
	default:
		return false
	}
}

// skipUnderCollectionCopyVerify reports whether the always-copy oracle is on,
// for the tests that measure what a write costs rather than what it means.
//
// The oracle copies and rebinds an addressable path on every write instead of
// asking whether each level is exclusively held. That is the point -- it is how
// a missed publish shows up as a semantic disagreement -- but it also changes
// every byte charged, every step taken, and every backing retained, so a test
// asserting a quota threshold, a linear step count, or a released backing has no
// stable answer under it. Those tests skip; the semantic ones, which are what
// the oracle exists to check, all run.
func skipUnderCollectionCopyVerify() bool { return alwaysCopyCollections }
