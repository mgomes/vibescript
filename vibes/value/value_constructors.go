package value

import (
	"math"
	"time"
	"unsafe"
)

// NewNil returns a nil Value.
func NewNil() Value { return Value{kind: KindNil} }

// NewBool returns a boolean Value.
func NewBool(b bool) Value {
	if b {
		return Value{kind: KindBool, scalar: 1}
	}
	return Value{kind: KindBool}
}

// NewInt returns an integer Value.
func NewInt(i int64) Value { return Value{kind: KindInt, scalar: uint64(i)} }

// NewFloat returns a floating-point Value.
func NewFloat(f float64) Value { return Value{kind: KindFloat, scalar: math.Float64bits(f)} }

// NewString returns a string Value.
func NewString(s string) Value { return Value{kind: KindString, data: s} }

// arrayData backs a KindArray value. Boxing the element slice behind a pointer
// gives every array a stable object identity and lets Ruby-style mutators
// (push, pop, clear, map!, ...) grow or shrink the array in place: every Value
// copy that aliases the same wrapper observes the mutation, matching Ruby's
// reference semantics for collections. (KindHash gets the same sharing from its
// *hashData wrapper.)
//
// head is how many element slots sit between the start of the slice the wrapper
// was first built over and the start of elems. A shrink that takes elements off
// the front hands the wrapper a window further into the same allocation, and Go
// keeps an allocation live as a whole for any pointer into it, so those slots
// are still held while nothing addresses them any more -- and a slice header
// says nothing about what is in front of it.
//
// Only the runtime's shrink maintains it, and only to decide when the vacated
// prefix has grown large enough to be worth copying the survivors out of.
// Nothing reads it as an accounting figure, so it is a cost heuristic rather
// than a fact: too large means one copy that was not needed, and too small --
// which is what a wrapper built over a slice that was already a window of
// something larger starts out with -- means a prefix the host put there stays
// held, exactly as it would have without any of this.
type arrayData struct {
	elems []Value
	head  int
}

// ArrayDataBytes is the heap footprint of the arrayData wrapper every KindArray
// value allocates, excluding the element backing it points at. Memory-quota
// accounting charges it once per distinct array so a workload retaining many
// small arrays cannot hold the per-array wrapper cost uncharged.
//
// It is derived from the struct rather than restated as a number so that a
// field added to arrayData is charged by the same commit that adds it. head was
// added without one, which took the wrapper from 24 bytes to 32 and left 8 of
// them unmetered per array -- an under-count introduced by a fix for an
// under-count. It is intended for the interpreter's internal use; hosts should
// not rely on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
const ArrayDataBytes = int(unsafe.Sizeof(arrayData{}))

// NewArray returns an array Value backed by a, which it takes ownership of.
//
// The slice must not be one another array Value is already built over. An
// in-place shrink clears the slots it vacates, so a second array over the same
// storage would find elements it still shows blanked out, and a growing push
// through one of them reallocates without the other seeing it. Build a second
// array over the same contents with a copy of the slice, not the slice.
func NewArray(a []Value) Value { return Value{kind: KindArray, data: &arrayData{elems: a}} }

// SetArrayElems replaces the element slice of an existing array wrapper in
// place. It is the primitive behind the runtime's Ruby-style mutators: because
// the wrapper is shared by every Value that aliases the array, the new elements
// are visible through all of them. It is a no-op when v is not an array.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) SetArrayElems(elems []Value) {
	if v.kind != KindArray {
		return
	}
	if ad, ok := v.data.(*arrayData); ok {
		BumpMutationEpoch()
		if !sameElemStart(ad.elems, elems) {
			// Elements that start somewhere else are a different allocation --
			// a mutator's freshly built slice, or an append that outgrew its
			// backing -- so whatever prefix the old one had is not in front of
			// this one. A window further into the same allocation must go
			// through SetArrayWindow, which says how far in it now starts.
			ad.head = 0
		}
		ad.elems = elems
	}
}

// SetArrayWindow narrows an array onto a window of the allocation its elements
// already sit in, recording how many slots of that allocation now sit in front
// of the window. head is counted from the start of the allocation, not from the
// elements the array showed before.
//
// It exists because a narrowed array goes on holding the whole allocation while
// the slice header it keeps describes less and less of it, and nothing about
// that header says how much is in front. It is intended for the interpreter's
// internal use; hosts should not call it, and it carries no compatibility
// promise (see docs/embedding-api-stability.md).
func (v Value) SetArrayWindow(elems []Value, head int) {
	BumpMutationEpoch()
	v.SetArrayWindowNoEpoch(elems, head)
}

// SetArrayWindowNoEpoch is SetArrayWindow without the process-wide
// mutation-epoch bump; see SetArrayElemsNoEpoch for the invariant the caller
// owes. It is intended for the interpreter's internal use; hosts should not
// call it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) SetArrayWindowNoEpoch(elems []Value, head int) {
	if v.kind != KindArray {
		return
	}
	if ad, ok := v.data.(*arrayData); ok {
		ad.elems = elems
		ad.head = head
	}
}

// ArrayWindowHead returns how many element slots an array's elements start
// past the beginning of the allocation they sit in, or 0 when v is not an
// array. It is intended for the interpreter's internal use; hosts should not
// call it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func ArrayWindowHead(v Value) int {
	if v.kind != KindArray {
		return 0
	}
	if ad, ok := v.data.(*arrayData); ok {
		return ad.head
	}
	return 0
}

// sameElemStart reports whether two element slices begin at the same address,
// which is what tells an in-place append from one that moved the elements to a
// new allocation. Two empty slices are not the same start: an empty one has no
// address to speak of, and a wrapper handed one is holding nothing whatever it
// held before.
func sameElemStart(old, next []Value) bool {
	if len(old) == 0 || len(next) == 0 {
		return false
	}
	return &old[0] == &next[0]
}

// SetArrayElemsNoEpoch replaces an array wrapper's element slice in place
// without bumping the process-wide mutation epoch, for callers that invalidate
// the memoized reachable-graph walk themselves. The interpreter uses it to
// charge the mutation to the one execution that can reach the array rather than
// to every execution in the process. Callers that do not invalidate must use
// SetArrayElems. It is intended for the interpreter's internal use; hosts
// should not call it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) SetArrayElemsNoEpoch(elems []Value) {
	if v.kind != KindArray {
		return
	}
	if ad, ok := v.data.(*arrayData); ok {
		if !sameElemStart(ad.elems, elems) {
			ad.head = 0
		}
		ad.elems = elems
	}
}

// AppendArrayElemNoEpoch appends elem to an array in place without bumping
// the mutation epoch. The epoch exists to invalidate memoized reachable-graph
// walks; the interpreter's charged-append path commits the element's bytes
// into that memo itself before appending, so the bump would only throw away
// accounting that is already correct. Callers that do not maintain the memo
// must use SetArrayElems. It is intended for the interpreter's internal use;
// hosts should not call it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) AppendArrayElemNoEpoch(elem Value) {
	if v.kind != KindArray {
		return
	}
	if ad, ok := v.data.(*arrayData); ok {
		grown := append(ad.elems, elem)
		if !sameElemStart(ad.elems, grown) {
			ad.head = 0
		}
		ad.elems = grown
	}
}

// ArrayIdentity returns an identity for an array wrapper, or 0 when v is not
// an array. It identifies the shared mutable wrapper itself rather than the
// current element backing, so identity survives in-place growth that reallocates
// the element slice. Two Values are the same array object exactly when their
// identities match.
//
// The identity is the wrapper's address as a bare uintptr, so it is only
// meaningful between captures taken while the address cannot move: Go may
// stack-allocate a wrapper that never escapes its function, and a goroutine
// stack growth then relocates it, changing the identity of a still-live array.
// The runtime only compares identities captured within a single traversal of a
// heap-reachable graph, where this cannot happen. It is intended for the
// interpreter's internal use; hosts should use Value.Identical, which compares
// live wrapper pointers, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func ArrayIdentity(v Value) uintptr {
	if v.kind != KindArray {
		return 0
	}
	if ad, ok := v.data.(*arrayData); ok {
		return uintptr(unsafe.Pointer(ad))
	}
	return 0
}

// hashData backs a KindHash value. It pairs the entry map with optional
// Ruby-style default metadata consulted on missing-key lookup: either a default
// value (returned without inserting) or a default proc (a KindBlock value the
// runtime invokes with the hash and key). KindObject keeps a bare map because
// objects never carry hash defaults.
//
// order records Ruby-style insertion order for typedEntries: HashSet appends
// each new lookup key and keeps an overwritten key at its original position,
// so when typedEntries is non-nil, order lists each of its keys exactly once.
// A hash promoted from a legacy string map seeds order from the sorted display
// keys because a bare Go map carries no insertion record. Legacy-only hashes
// (typedEntries == nil) keep a nil order and iterate in sorted key order.
type hashData struct {
	entries            map[string]Value
	typedEntries       map[HashLookupKey]HashEntry
	typedEntryCapacity int
	order              []HashLookupKey
	defaultValue       Value
	defaultProc        Value
}

// HashDataBytes is the heap footprint of the hashData wrapper every KindHash
// value allocates, excluding the entry map and default payloads it points at.
// Memory-quota accounting charges it once per distinct hash so an array of many
// small hashes cannot retain the per-hash wrapper cost uncharged.
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
const HashDataBytes = int(unsafe.Sizeof(hashData{}))

// NewHash returns a hash (map) Value with no default.
func NewHash(h map[string]Value) Value {
	return Value{kind: KindHash, data: &hashData{entries: h}}
}

// NewTypedHash returns a hash with typed-key storage and no materialized legacy
// string-key map. Hash() materializes that map lazily for legacy callers.
func NewTypedHash(capacity int) Value {
	var order []HashLookupKey
	if capacity > 0 {
		order = make([]HashLookupKey, 0, capacity)
	}
	return Value{kind: KindHash, data: &hashData{
		typedEntries:       make(map[HashLookupKey]HashEntry, capacity),
		typedEntryCapacity: capacity,
		order:              order,
	}}
}

// NewHashWithDefault returns a hash (map) Value carrying Ruby-style default
// metadata. A non-nil defaultProc (a KindBlock value) takes precedence over
// defaultValue on missing-key lookup; pass NewNil() for whichever is unused.
func NewHashWithDefault(h map[string]Value, defaultValue, defaultProc Value) Value {
	return Value{kind: KindHash, data: &hashData{
		entries:      h,
		defaultValue: defaultValue,
		defaultProc:  defaultProc,
	}}
}

// SetHashDefaults overwrites the Ruby-style default metadata of an existing hash
// wrapper in place. It exists so a deep clone can register the destination
// wrapper in its seen-set before it walks the default value/proc: a default that
// reaches the hash itself (e.g. Hash.new { |_, _| h }) then dedups against the
// already-registered wrapper instead of cloning a second one whose defaults
// would close over the wrong object. v must be a hash whose wrapper is not yet
// shared; mutating a hash that other Values observe would change their defaults.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) SetHashDefaults(defaultValue, defaultProc Value) {
	v.setHashDefaultsInternal(defaultValue, defaultProc, true)
}

// SetHashDefaultsUnpublished is SetHashDefaults without the process-wide
// mutation-epoch bump, for a hash still reachable from no execution root. See
// HashSetUnpublished for the invariant the caller owes. It is intended for the
// interpreter's internal use; hosts should not call it, and it carries no
// compatibility promise (see docs/embedding-api-stability.md).
func (v Value) SetHashDefaultsUnpublished(defaultValue, defaultProc Value) {
	v.setHashDefaultsInternal(defaultValue, defaultProc, false)
}

func (v Value) setHashDefaultsInternal(defaultValue, defaultProc Value, bump bool) {
	if v.kind != KindHash {
		return
	}
	if hd, ok := v.data.(*hashData); ok {
		if bump {
			BumpMutationEpoch()
		}
		hd.defaultValue = defaultValue
		hd.defaultProc = defaultProc
	}
}

// HashDefaultValue returns the default value configured for a hash, or NewNil()
// when v is not a hash or carries no default value. It is the plain-value
// counterpart to HashDefaultProc.
func HashDefaultValue(v Value) Value {
	if v.kind != KindHash {
		return NewNil()
	}
	if hd, ok := v.data.(*hashData); ok {
		return hd.defaultValue
	}
	return NewNil()
}

// HashDefaultProc returns the default proc configured for a hash, or NewNil()
// when v is not a hash or carries no default proc. The returned value, when
// present, is the KindBlock the runtime invokes on missing-key lookup.
func HashDefaultProc(v Value) Value {
	if v.kind != KindHash {
		return NewNil()
	}
	if hd, ok := v.data.(*hashData); ok {
		return hd.defaultProc
	}
	return NewNil()
}

// HashIdentity returns an identity for a hash wrapper, or 0 when v is not
// a hash. Unlike the entry-map pointer, this identifies the whole hashData
// wrapper, so two KindHash values that share an entry map but carry different
// default metadata are distinct. Cycle-detecting scanners that must also visit
// hash defaults key their seen-set on this value rather than the bare entry map,
// which would otherwise hide a second wrapper's distinct default payload.
//
// Like ArrayIdentity, the identity is a bare uintptr wrapper address and is
// only meaningful between captures taken while the address cannot move (see
// ArrayIdentity for the stack-allocation caveat). It is intended for the
// interpreter's internal use; hosts should use Value.Identical, and it carries
// no compatibility promise (see docs/embedding-api-stability.md).
func HashIdentity(v Value) uintptr {
	if v.kind != KindHash {
		return 0
	}
	if hd, ok := v.data.(*hashData); ok {
		return uintptr(unsafe.Pointer(hd))
	}
	return 0
}

// ObjectIdentity returns the identity of the objectData wrapper a KindObject
// value allocates, or 0 for anything else. Each wrapper is a distinct
// allocation even when several share one entry map, so the sandbox's memory
// accounting deduplicates on this rather than on the map.
//
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func ObjectIdentity(v Value) uintptr {
	if v.kind != KindObject {
		return 0
	}
	if od, ok := v.data.(*objectData); ok {
		return uintptr(unsafe.Pointer(od))
	}
	return 0
}

// NewMoney returns a money Value.
func NewMoney(m Money) Value { return Value{kind: KindMoney, data: m} }

// NewDuration returns a duration Value.
func NewDuration(d Duration) Value {
	return Value{kind: KindDuration, scalar: uint64(d.Seconds())}
}

// NewTime returns a time Value.
func NewTime(t time.Time) Value { return Value{kind: KindTime, data: t} }

// NewSymbol returns a symbol Value.
func NewSymbol(name string) Value { return Value{kind: KindSymbol, data: name} }

// NewObject returns an object Value with the given attributes. A nil map is
// replaced with a freshly allocated empty map so that every object has its own
// backing storage and thus a distinct object identity. (Hashes get the same
// per-instance identity from their hashData wrapper, which NewHash allocates
// fresh on every call.)
func NewObject(attrs map[string]Value) Value {
	if attrs == nil {
		attrs = map[string]Value{}
	}
	return Value{kind: KindObject, data: &objectData{entries: attrs}}
}

// objectData is a KindObject payload: the entry map plus, for the few bags the
// runtime builds to stand for something specific, the provenance and the
// string form fixed at construction.
//
// The string form is stored rather than read back out of the entries because
// the entries are mutable and reachable through the public API -- Value.Hash()
// hands out the live map, and a host builtin receives the value itself. A
// rendering derived from the entries could therefore be rewritten by anything
// holding the bag, which is exactly the spoof the provenance exists to
// prevent. Fixing it at construction makes the rendering immutable no matter
// who mutates the map afterwards.
type objectData struct {
	entries    map[string]Value
	tag        ObjectTag
	stringForm string
}

// ObjectTag records what an attribute bag is, for the few bags the runtime
// builds to stand for something specific.
//
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md). Behavior that would otherwise have
// to be inferred from a bag's field names reads the tag instead: field names
// are public, host-settable data, so any bag could carry the same ones and
// claim the same treatment.
type ObjectTag uint8

const (
	// ObjectTagNone marks an ordinary attribute bag, which is every bag
	// NewObject builds. It is the zero value, so untagged is the default.
	ObjectTagNone ObjectTag = iota
	// ObjectTagRescuedError marks the bag a rescue binds, whose to_s is the
	// error message.
	ObjectTagRescuedError
	// ObjectTagMatchData marks the bag a regexp match returns, whose to_s is
	// the matched text as in Ruby's MatchData.
	ObjectTagMatchData
)

// NewTaggedObject returns an attribute bag carrying provenance. The tag rides
// in the scalar word, which an object otherwise leaves unused, so it costs
// nothing and cannot appear as an entry: it is invisible to keys, values,
// inspect, and JSON, and script code has no way to set it.
//
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func NewTaggedObject(attrs map[string]Value, tag ObjectTag, stringForm string) Value {
	if attrs == nil {
		attrs = map[string]Value{}
	}
	return Value{kind: KindObject, data: &objectData{entries: attrs, tag: tag, stringForm: stringForm}}
}

// ObjectTag reports the provenance of an attribute bag, or ObjectTagNone for
// anything else.
//
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md). A bag that has been rebuilt (merged, duplicated, or produced
// by host code) reports ObjectTagNone, so the tag only ever vouches for a bag
// the runtime built itself.
func (v Value) ObjectTag() ObjectTag {
	if v.kind != KindObject {
		return ObjectTagNone
	}
	return v.data.(*objectData).tag
}

// ObjectStringForm returns the rendering a tagged bag published at
// construction, and reports false for an ordinary bag. It is fixed then and
// never read back out of the entries, so mutating them cannot change it.
//
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) ObjectStringForm() (string, bool) {
	if v.kind != KindObject {
		return "", false
	}
	obj := v.data.(*objectData)
	if obj.tag == ObjectTagNone {
		return "", false
	}
	return obj.stringForm, true
}

// NewRange returns a range Value.
func NewRange(r Range) Value { return Value{kind: KindRange, data: r} }

// ObjectDataBytes is the heap footprint of the objectData wrapper every
// KindObject value allocates around its entry map, for the sandbox's memory
// accounting.
//
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
const ObjectDataBytes = int(unsafe.Sizeof(objectData{}))
