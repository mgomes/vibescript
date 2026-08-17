package value

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
)

// HashEntry is one hash entry. Hash keys live in one string keyspace, so Key is
// always a KindString value.
type HashEntry struct {
	Key   Value
	Value Value
}

// HashKeyString returns the entry a hash key value addresses. Strings and
// symbols are the only accepted key inputs and a symbol normalizes to its name,
// so `hash["name"]`, `hash[:name]`, and the literal label `name:` all address
// one entry. Every other kind is rejected, and the error names what is
// accepted.
func HashKeyString(key Value) (string, error) {
	switch key.kind {
	case KindString, KindSymbol:
		return key.data.(string), nil
	default:
		return "", fmt.Errorf("unsupported hash key type %s: hash keys must be strings or symbols; convert the key with to_s", key.kind)
	}
}

// HashLen returns the number of entries in a hash or object.
func (v Value) HashLen() int {
	switch v.kind {
	case KindHash:
		return len(v.data.(*hashData).entries)
	case KindObject:
		return len(v.data.(*objectData).entries)
	default:
		return 0
	}
}

// HashEntries returns hash entries in iteration order. Objects are exposed as
// string-keyed entries in sorted key order.
func (v Value) HashEntries() []HashEntry {
	return v.HashEntriesInto(nil)
}

// HashEntriesInto appends hash entries into buf when it has enough capacity.
// Entries appear in the order the hash iterates: Ruby-style insertion order for
// a hash built through HashSet, and sorted key order for a bare map handed in by
// a host, which records no insertion order. The copy snapshots the entries, so a
// script block that mutates the receiver mid-iteration cannot skew the walk.
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) HashEntriesInto(buf []HashEntry) []HashEntry {
	m := v.hashEntryMap()
	if m == nil {
		return nil
	}
	entries := buf[:0]
	if cap(entries) < len(m) {
		entries = make([]HashEntry, 0, len(m))
	}
	if v.kind == KindHash {
		hd := v.data.(*hashData)
		if hd.orderCoversEntries() {
			for _, key := range hd.order {
				entries = append(entries, HashEntry{Key: key, Value: m[key.data.(string)]})
			}
			return entries
		}
	}
	// Sort the entry buffer in place. Building a separate key slice here
	// would allocate on top of entries, and the runtime's hash walks only
	// preflight the entry buffer (sortedHashEntryBufferBytes).
	for key, val := range m {
		entries = append(entries, HashEntry{Key: NewString(key), Value: val})
	}
	slices.SortFunc(entries, func(a, b HashEntry) int {
		return cmp.Compare(a.Key.data.(string), b.Key.data.(string))
	})
	return entries
}

// RangeHashEntries calls visit for each entry of v in iteration order without
// materializing an intermediate slice. It is a no-op for non-hash values.
// Callers that only need a read-only pass (memory estimation) use it to avoid
// the per-call slice HashEntriesInto allocates for a hash larger than the
// caller's buffer. Because it holds no shared state it is safe to nest, which a
// buffer-reusing variant would not be. A visit that mutates v is undefined, so
// callers that can re-enter script code snapshot with HashEntriesInto instead.
// It is intended for the interpreter's internal use; hosts should not call it,
// and it carries no compatibility promise (see docs/embedding-api-stability.md).
func (v Value) RangeHashEntries(visit func(key string, val Value)) {
	m := v.hashEntryMap()
	if m == nil {
		return
	}
	if v.kind == KindHash {
		hd := v.data.(*hashData)
		if hd.orderCoversEntries() {
			for _, key := range hd.order {
				name := key.data.(string)
				visit(name, m[name])
			}
			return
		}
	}
	// Fallback walks the live map without allocating a key slice. Callers
	// that need a sorted snapshot (JSON.stringify) sort their own buffer.
	for key, val := range m {
		visit(key, val)
	}
}

// HashKeyOrder returns a fresh snapshot of the key order v iterates in. It
// pairs with NewHashWithOrder so a copier that clones an entry map wholesale
// can give the copy its source's iteration order.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) HashKeyOrder() []Value {
	return slices.Clone(v.hashIterationKeys(nil))
}

// hashEntryMap returns the live entry map of a hash or object, or nil for
// anything else.
func (v Value) hashEntryMap() map[string]Value {
	switch v.kind {
	case KindHash:
		return v.data.(*hashData).entries
	case KindObject:
		return v.data.(*objectData).entries
	default:
		return nil
	}
}

// hashIterationKeys returns the keys of a hash or object in iteration order,
// appending into buf when it has room. A hash whose recorded insertion order
// still covers its entries iterates in that order; anything else — an object,
// or a bare map a host handed to NewHash and then mutated through Hash() —
// iterates in sorted key order, so iteration is deterministic rather than
// exposing Go's randomized map traversal.
//
// The order slots hold the key Values themselves rather than bare strings, so
// walking a hash hands out the stored key instead of boxing a fresh string
// Value per entry on every iteration.
//
// Matching lengths alone would not establish that the record is current: Hash()
// hands a host the live entry map, and swapping one key for another through it
// leaves the count unchanged while the record names a key the map no longer
// holds. The order keys are unique by construction (HashSet appends only for an
// absent key), so a record whose keys are all present and whose length matches
// is exactly the map's key set; probing membership is therefore a complete
// check, not a heuristic.
func (v Value) hashIterationKeys(buf []Value) []Value {
	if v.kind == KindHash {
		hd := v.data.(*hashData)
		if hd.orderCoversEntries() {
			return hd.order
		}
	}
	return sortedMapKeysInto(v.hashEntryMap(), buf)
}

// orderCoversEntries reports whether the recorded order still names exactly the
// live entry set. See hashIterationKeys for why membership plus length is
// exact once HashSet refuses to append a name that is already recorded.
func (hd *hashData) orderCoversEntries() bool {
	if len(hd.order) != len(hd.entries) {
		return false
	}
	for i := range hd.order {
		if _, ok := hd.entries[hd.order[i].data.(string)]; !ok {
			return false
		}
	}
	return true
}

// reconcileRecordedOrder drops order slots the live map no longer holds.
// Hash() can leave the record stale; doing this once on the next write
// restores the append-only invariant without a linear scan per insert.
func (hd *hashData) reconcileRecordedOrder() {
	if hd.orderCoversEntries() {
		return
	}
	kept := hd.order[:0]
	for _, key := range hd.order {
		if _, ok := hd.entries[key.data.(string)]; ok {
			kept = append(kept, key)
		}
	}
	hd.order = kept
}

func sortedMapKeysInto(m map[string]Value, buf []Value) []Value {
	keys := buf[:0]
	if cap(keys) < len(m) {
		keys = make([]Value, 0, len(m))
	}
	for key := range m {
		keys = append(keys, NewString(key))
	}
	slices.SortFunc(keys, func(a, b Value) int {
		return cmp.Compare(a.data.(string), b.data.(string))
	})
	return keys
}

// HashGet returns the value for key from a hash or object. A key that is
// neither a string nor a symbol is rejected rather than reported as a miss.
func (v Value) HashGet(key Value) (Value, bool, error) {
	m := v.hashEntryMap()
	if m == nil {
		return NewNil(), false, nil
	}
	name, err := HashKeyString(key)
	if err != nil {
		return NewNil(), false, err
	}
	val, ok := m[name]
	return val, ok, nil
}

// HashSet stores key/value in a hash or object. On a hash it preserves
// Ruby-style insertion order: a new key is appended to the recorded order and
// an existing key keeps its original position while taking the new value.
func (v Value) HashSet(key, val Value) error {
	return v.hashSetInternal(key, val, true)
}

// HashSetUnpublished is HashSet without the mutation-epoch bump. The epoch
// exists solely to invalidate memoized reachable-graph walks, and a write
// into a container reachable from no execution root cannot stale one, so the
// interpreter's literal builder uses this while assembling a hash that
// nothing references yet. The caller must guarantee the hash is unreachable
// from every root until a publishing write (an env bind, a container store,
// an ivar store) bumps the epoch — exactly the discipline array literals
// already follow, since building a Go-local slice never bumps at all. It is
// intended for the interpreter's internal use; hosts should not call it, and
// it carries no compatibility promise (see docs/embedding-api-stability.md).
func (v Value) HashSetUnpublished(key, val Value) error {
	return v.hashSetInternal(key, val, false)
}

func (v Value) hashSetInternal(key, val Value, bump bool) error {
	switch v.kind {
	case KindHash, KindObject:
	default:
		return fmt.Errorf("%s is not a hash", v.kind)
	}
	name, err := HashKeyString(key)
	if err != nil {
		return err
	}
	if v.kind == KindObject {
		BumpMutationEpoch()
		v.data.(*objectData).entries[name] = val
		return nil
	}
	hd := v.data.(*hashData)
	if bump {
		BumpMutationEpoch()
	}
	if hd.entries == nil {
		hd.entries = make(map[string]Value)
	}
	if _, exists := hd.entries[name]; !exists {
		// Store the normalized key Value so iteration hands out this one rather
		// than boxing a fresh string Value per entry per walk. A key that is
		// already a string is stored as-is, so copying entries between hashes
		// (select, merge, transform_values) boxes nothing at all; only a symbol
		// pays one boxing as it normalizes.
		//
		// After Hash() a host may have deleted this name from the live map
		// while it is still in order. Reconcile once so the next append cannot
		// duplicate it, then trusted HashSet traffic stays an O(1) map probe.
		if hd.orderUntrusted.Load() {
			hd.reconcileRecordedOrder()
			hd.orderUntrusted.Store(false)
		}
		keyValue := key
		if key.kind != KindString {
			keyValue = NewString(name)
		}
		hd.order = append(hd.order, keyValue)
	}
	hd.entries[name] = val
	if len(hd.entries) > hd.entryCapacity {
		hd.entryCapacity = len(hd.entries)
	}
	return nil
}

// HashDeleteKey removes the entry for key from a hash or object in place,
// returning the removed value and whether the key was present. On a hash it
// also removes the key's slot from the recorded insertion order, keeping the
// order/entries invariant HashSet maintains. A missing key leaves the hash
// untouched. An error is returned only for an unsupported key.
func (v Value) HashDeleteKey(key Value) (Value, bool, error) {
	m := v.hashEntryMap()
	if m == nil {
		return NewNil(), false, nil
	}
	name, err := HashKeyString(key)
	if err != nil {
		return NewNil(), false, err
	}
	val, ok := m[name]
	if !ok {
		return NewNil(), false, nil
	}
	BumpMutationEpoch()
	delete(m, name)
	if v.kind == KindHash {
		hd := v.data.(*hashData)
		i := slices.IndexFunc(hd.order, func(key Value) bool {
			return key.data.(string) == name
		})
		if i >= 0 {
			hd.order = append(hd.order[:i], hd.order[i+1:]...)
		}
	}
	return val, true, nil
}

// HashClearEntries removes every entry from a hash or object in place. A hash's
// entry map and insertion order are replaced with fresh empty storage so the
// old backings are released.
func (v Value) HashClearEntries() {
	switch v.kind {
	case KindHash:
		hd := v.data.(*hashData)
		BumpMutationEpoch()
		if hd.entries != nil {
			hd.entries = map[string]Value{}
		}
		hd.entryCapacity = 0
		hd.order = nil
		hd.orderUntrusted.Store(false)
	case KindObject:
		BumpMutationEpoch()
		clear(v.data.(*objectData).entries)
	}
}

// HashUsesRecordedOrder reports whether v iterates in a recorded insertion
// order. Objects and hashes whose live map no longer matches that record
// iterate in sorted key order instead.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) HashUsesRecordedOrder() bool {
	if v.kind != KindHash {
		return false
	}
	return v.data.(*hashData).orderCoversEntries()
}

// HashOrderCapacity returns the capacity of the insertion-order backing a hash
// retains alongside its entries, or 0 when v is not a hash or tracks no order.
// Memory-quota accounting charges the backing's structural bytes; the key
// Values inside it alias key strings the entry storage already charges.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func HashOrderCapacity(v Value) int {
	if v.kind != KindHash {
		return 0
	}
	if hd, ok := v.data.(*hashData); ok {
		return cap(hd.order)
	}
	return 0
}

// HashEntryCapacity returns the minimum entry-map capacity the hash is known to
// retain. Go does not expose current map bucket capacity, so this tracks
// explicit reservations plus the live entry count reached through HashSet.
// Memory-quota estimation uses it to charge reserved buckets that may exceed
// the live entry count.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func HashEntryCapacity(v Value) int {
	if v.kind != KindHash {
		return 0
	}
	if hd, ok := v.data.(*hashData); ok {
		return max(hd.entryCapacity, len(hd.entries))
	}
	return 0
}

// ReserveHashOrder pre-sizes the insertion-order backing to capacity n so a
// builder that knows its final entry count avoids the append growth overshoot,
// where a hash of 3 entries would otherwise retain 4 order slots. This keeps the
// backing's capacity equal to the entry count the memory-quota projection
// charges. It does not pre-size the entry map, so hash literals can reserve
// order capacity without allocating buckets before their per-entry accounting
// runs. It is a no-op when v is not a hash, n is non-positive, or the backing
// already has at least n slots.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) ReserveHashOrder(n int) {
	v.reserveHashOrderInternal(n, true)
}

// ReserveHashOrderUnpublished is ReserveHashOrder without the mutation-epoch
// bump, for a hash still reachable from no execution root. See
// HashSetUnpublished for the invariant the caller owes.
func (v Value) ReserveHashOrderUnpublished(n int) {
	v.reserveHashOrderInternal(n, false)
}

func (v Value) reserveHashOrderInternal(n int, bump bool) {
	if v.kind != KindHash || n <= 0 {
		return
	}
	hd, ok := v.data.(*hashData)
	if !ok || cap(hd.order) >= n {
		return
	}
	if bump {
		BumpMutationEpoch()
	}
	grown := make([]Value, len(hd.order), n)
	copy(grown, hd.order)
	hd.order = grown
}

// ReserveHashCapacity pre-sizes a hash's entry map and insertion-order backing
// to n entries. It is a no-op for non-hashes and negative capacities.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) ReserveHashCapacity(n int) {
	if v.kind != KindHash || n < 0 {
		return
	}
	hd, ok := v.data.(*hashData)
	if !ok {
		return
	}
	if hd.entries == nil {
		BumpMutationEpoch()
		hd.entries = make(map[string]Value, n)
		hd.entryCapacity = n
		v.ReserveHashOrder(n)
		return
	}
	if n > hd.entryCapacity {
		BumpMutationEpoch()
		grown := make(map[string]Value, n)
		maps.Copy(grown, hd.entries)
		hd.entries = grown
		hd.entryCapacity = n
	}
	v.ReserveHashOrder(n)
}

// smallHashIterationBuffer sizes the inline key buffer the rendering and
// projection walks pass to hashIterationKeys. A hash iterating its recorded
// order ignores the buffer entirely; only the sorted fallback fills it, and
// most script hashes are small records that fit.
const smallHashIterationBuffer = 8
