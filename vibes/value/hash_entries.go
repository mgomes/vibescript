package value

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// HashEntry is one hash entry with the original script key preserved.
type HashEntry struct {
	Key   Value
	Value Value
}

// TypedHashEntry is a typed hash entry paired with its stored lookup key.
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
type TypedHashEntry struct {
	LookupKey HashLookupKey
	Entry     HashEntry
}

// HashLookupKey is a comparable hash-key identity used for hash table lookups.
// It preserves Ruby-style key identity without materializing canonical strings
// for scalar keys on hot paths.
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
type HashLookupKey struct {
	kind           ValueKind
	text           string
	number         int64
	bits           uint64
	flag           bool
	rangeEnd       int64
	rangeExclusive bool
	rangeBeginless bool
	rangeEndless   bool
}

// NewHashLookupKey returns the comparable lookup key for a hash key value.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func NewHashLookupKey(key Value) (HashLookupKey, error) {
	switch key.kind {
	case KindNil:
		return HashLookupKey{kind: KindNil}, nil
	case KindBool:
		return HashLookupKey{kind: KindBool, flag: key.Bool()}, nil
	case KindInt:
		// A big integer canonicalizes to its hexadecimal text: hex conversion
		// is linear in the payload's words, where decimal is superlinear, so a
		// hostile key cannot hide unbounded conversion CPU here (runtime
		// callers also charge steps per key word). The canonical invariant
		// (big payloads never fit int64) keeps the text form disjoint from
		// compact keys, which stay in the numeric field with empty text, so
		// the two encodings can never collide.
		if bi, ok := BigIntPayload(key); ok {
			return HashLookupKey{kind: KindInt, text: bi.Text(16)}, nil
		}
		return HashLookupKey{kind: KindInt, number: key.Int()}, nil
	case KindFloat:
		f := key.Float()
		if math.IsNaN(f) {
			return HashLookupKey{}, fmt.Errorf("unsupported hash key float NaN")
		}
		if f == 0 {
			f = 0
		}
		return HashLookupKey{kind: KindFloat, bits: math.Float64bits(f)}, nil
	case KindString:
		return HashLookupKey{kind: KindString, text: key.String()}, nil
	case KindSymbol:
		return HashLookupKey{kind: KindSymbol, text: key.String()}, nil
	case KindRange:
		r := key.Range()
		return HashLookupKey{
			kind:           KindRange,
			number:         r.Start,
			rangeEnd:       r.End,
			rangeExclusive: r.Exclusive,
			rangeBeginless: r.Beginless,
			rangeEndless:   r.Endless,
		}, nil
	case KindArray:
		canonical, err := HashKey(key)
		if err != nil {
			return HashLookupKey{}, err
		}
		return HashLookupKey{kind: KindArray, text: canonical}, nil
	default:
		return HashLookupKey{}, fmt.Errorf("unsupported hash key type %s", key.kind)
	}
}

// ExtraPayloadBytes returns heap bytes stored only by this lookup key, excluding
// the fixed HashLookupKey struct itself. Scalar lookup keys either keep their
// payload in numeric fields or alias the original key value's string payload;
// array keys and big-integer keys retain a canonical lookup string that is not
// reachable otherwise.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (k HashLookupKey) ExtraPayloadBytes() int {
	switch k.kind {
	case KindArray:
		return len(k.text)
	case KindInt:
		// Only big-integer keys carry text (compact keys ride in the numeric
		// field), so this charges exactly the materialized decimal form.
		return len(k.text)
	default:
		return 0
	}
}

// HashKey returns the canonical lookup key for a hash key value. The encoding
// is an internal detail of the hash tables; hosts look entries up with HashGet
// and read original keys from HashEntries. It is intended for the interpreter's
// internal use; hosts should not call it, and it carries no compatibility
// promise (see docs/embedding-api-stability.md).
func HashKey(key Value) (string, error) {
	return hashKey(key, make(map[SliceIdentity]struct{}))
}

func hashKey(key Value, arrays map[SliceIdentity]struct{}) (string, error) {
	switch key.kind {
	case KindNil:
		return "nil", nil
	case KindBool:
		if key.Bool() {
			return "bool:true", nil
		}
		return "bool:false", nil
	case KindInt:
		// Big integers canonicalize under their own "bigint:" prefix in
		// hexadecimal: hex conversion is linear in the payload's words, where
		// decimal is superlinear, and no other key space produces the prefix.
		// The canonical invariant keeps big and compact int keys in disjoint
		// value spaces, so splitting the prefixes never separates equal values.
		if bi, ok := BigIntPayload(key); ok {
			return "bigint:" + bi.Text(16), nil
		}
		return "int:" + strconv.FormatInt(key.Int(), 10), nil
	case KindFloat:
		f := key.Float()
		if math.IsNaN(f) {
			return "", fmt.Errorf("unsupported hash key float NaN")
		}
		if f == 0 {
			f = 0
		}
		return "float:" + strconv.FormatUint(math.Float64bits(f), 16), nil
	case KindString:
		return "string:" + encodeHashKeyString(key.String()), nil
	case KindSymbol:
		return "symbol:" + encodeHashKeyString(key.String()), nil
	case KindArray:
		arr := key.Array()
		id := SliceIdentity{
			Ptr: reflect.ValueOf(arr).Pointer(),
			Len: len(arr),
			Cap: cap(arr),
		}
		if id.Ptr != 0 {
			if _, ok := arrays[id]; ok {
				return "", fmt.Errorf("unsupported cyclic array hash key")
			}
			arrays[id] = struct{}{}
			defer delete(arrays, id)
		}
		var b strings.Builder
		b.WriteString("array:")
		b.WriteString(strconv.Itoa(len(arr)))
		b.WriteByte(':')
		for _, elem := range arr {
			encoded, err := hashKey(elem, arrays)
			if err != nil {
				return "", err
			}
			b.WriteString(strconv.Itoa(len(encoded)))
			b.WriteByte(':')
			b.WriteString(encoded)
		}
		return b.String(), nil
	case KindRange:
		r := key.Range()
		start, end := strconv.FormatInt(r.Start, 10), strconv.FormatInt(r.End, 10)
		// Open endpoints canonicalize as empty bounds so (1..) can never
		// collide with any bounded range sharing its start.
		if r.Beginless {
			start = ""
		}
		if r.Endless {
			end = ""
		}
		return "range:" + start + ":" + end + ":" + strconv.FormatBool(r.Exclusive), nil
	default:
		return "", fmt.Errorf("unsupported hash key type %s", key.kind)
	}
}

func encodeHashKeyString(s string) string {
	return strconv.Itoa(len(s)) + ":" + s
}

// HashDisplayKey returns the legacy string-map key used by Hash() for callers
// that inspect ordinary hashes through the public map API. The encoding is not
// frozen; hosts that need original keys or reliable lookups use HashEntries and
// HashGet. It is intended for the interpreter's internal use; hosts should not
// call it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func HashDisplayKey(key Value) string {
	switch key.kind {
	case KindString, KindSymbol:
		return key.String()
	default:
		return key.Inspect()
	}
}

// HashLen returns the number of entries in a hash or object.
func (v Value) HashLen() int {
	switch v.kind {
	case KindHash:
		hd := v.data.(*hashData)
		if hd.typedEntries != nil {
			return len(hd.typedEntries)
		}
		return len(hd.entries)
	case KindObject:
		return len(v.data.(*objectData).entries)
	default:
		return 0
	}
}

// HashHasTypedEntries reports whether a hash carries canonical typed-key
// entries in addition to the legacy string-key map exposed by Hash().
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) HashHasTypedEntries() bool {
	if v.kind != KindHash {
		return false
	}
	hd := v.data.(*hashData)
	return hd.typedEntries != nil
}

// HashEntries returns hash entries with original keys preserved. Objects are
// exposed as string-keyed entries.
func (v Value) HashEntries() []HashEntry {
	return v.HashEntriesInto(nil)
}

// HashEntriesInto appends hash entries with original keys preserved into buf
// when it has enough capacity. Typed entries appear in Ruby-style insertion
// order; legacy string-map entries appear in Go map order (callers that need
// determinism for legacy hashes sort by key themselves).
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) HashEntriesInto(buf []HashEntry) []HashEntry {
	switch v.kind {
	case KindHash:
		hd := v.data.(*hashData)
		if hd.typedEntries != nil {
			entries := buf[:0]
			if cap(entries) < len(hd.typedEntries) {
				entries = make([]HashEntry, 0, len(hd.typedEntries))
			}
			if len(hd.order) == len(hd.typedEntries) {
				for _, lookupKey := range hd.order {
					entries = append(entries, hd.typedEntries[lookupKey])
				}
				return entries
			}
			for _, entry := range hd.typedEntries {
				entries = append(entries, entry)
			}
			return entries
		}
		entries := buf[:0]
		if cap(entries) < len(hd.entries) {
			entries = make([]HashEntry, 0, len(hd.entries))
		}
		for key, val := range hd.entries {
			entries = append(entries, HashEntry{Key: NewString(key), Value: val})
		}
		return entries
	case KindObject:
		obj := v.data.(*objectData).entries
		entries := buf[:0]
		if cap(entries) < len(obj) {
			entries = make([]HashEntry, 0, len(obj))
		}
		for key, val := range obj {
			entries = append(entries, HashEntry{Key: NewString(key), Value: val})
		}
		return entries
	default:
		return nil
	}
}

// TypedHashEntriesInto appends typed hash entries and their stored lookup keys
// into buf. It returns nil for non-hash values and hashes that still use only
// the legacy string-key map.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) TypedHashEntriesInto(buf []TypedHashEntry) []TypedHashEntry {
	if v.kind != KindHash {
		return nil
	}
	hd := v.data.(*hashData)
	if hd.typedEntries == nil {
		return nil
	}

	entries := buf[:0]
	if cap(entries) < len(hd.typedEntries) {
		entries = make([]TypedHashEntry, 0, len(hd.typedEntries))
	}
	for lookupKey, entry := range hd.typedEntries {
		entries = append(entries, TypedHashEntry{LookupKey: lookupKey, Entry: entry})
	}
	return entries
}

// RangeTypedHashEntries calls visit for each typed entry of v in place, without
// materializing an intermediate slice. It is a no-op for non-hash values and
// for hashes still using the legacy string-key map. Callers that only need a
// read-only pass over entries (memory estimation) use it to avoid the
// per-call slice TypedHashEntriesInto allocates for a hash larger than the
// caller's buffer. Because it holds no shared state it is safe to nest, which a
// buffer-reusing variant would not be.
// It is intended for the interpreter's internal use; hosts should not call it,
// and it carries no compatibility promise (see docs/embedding-api-stability.md).
func (v Value) RangeTypedHashEntries(visit func(lookupKey HashLookupKey, entry HashEntry)) {
	if v.kind != KindHash {
		return
	}
	hd := v.data.(*hashData)
	if hd.typedEntries == nil {
		return
	}
	for lookupKey, entry := range hd.typedEntries {
		visit(lookupKey, entry)
	}
}

// HashGet returns the value for key from a hash or object.
func (v Value) HashGet(key Value) (Value, bool, error) {
	switch v.kind {
	case KindHash:
		hd := v.data.(*hashData)
		if hd.typedEntries != nil {
			canonical, err := NewHashLookupKey(key)
			if err != nil {
				return NewNil(), false, err
			}
			if entry, ok := hd.typedEntries[canonical]; ok {
				return entry.Value, true, nil
			}
			return NewNil(), false, nil
		}
		if key.kind != KindString && key.kind != KindSymbol {
			return NewNil(), false, nil
		}
		val, ok := hd.entries[key.String()]
		return val, ok, nil
	case KindObject:
		if key.kind != KindString && key.kind != KindSymbol {
			return NewNil(), false, nil
		}
		val, ok := v.data.(*objectData).entries[key.String()]
		return val, ok, nil
	default:
		return NewNil(), false, nil
	}
}

// HashSet stores key/value in a hash or object. On a hash it preserves
// Ruby-style insertion order: a new key is appended to the recorded order and
// an existing key keeps its original position while taking the new value.
func (v Value) HashSet(key, val Value) error {
	switch v.kind {
	case KindHash:
		hd := v.data.(*hashData)
		BumpMutationEpoch()
		if hd.typedEntries == nil {
			if hd.entries == nil {
				hd.entries = make(map[string]Value)
			}
			hd.typedEntries = make(map[HashLookupKey]HashEntry, len(hd.entries))
			hd.typedEntryCapacity = len(hd.entries)
			// A legacy string map records no insertion order, so promotion seeds
			// the order from the sorted display keys: the hash keeps the
			// deterministic sorted iteration it always had, and only keys inserted
			// from here on append in insertion order. Sort the order backing in
			// place — each promoted lookup key carries its display string in text —
			// rather than a separate key slice, so promotion adds no scratch a
			// caller without an Execution context could not charge. Honor a
			// capacity a builder reserved up front (ReserveHashOrder or
			// ReserveTypedHashOrder); a legacy-only hash has none, so this
			// allocates the backing as before.
			if cap(hd.order) < len(hd.entries)+1 {
				hd.order = make([]HashLookupKey, 0, len(hd.entries)+1)
			} else {
				hd.order = hd.order[:0]
			}
			for displayKey, entryVal := range hd.entries {
				entryKey := promotedLegacyHashKey(displayKey, key)
				canonical, err := NewHashLookupKey(entryKey)
				if err != nil {
					return err
				}
				hd.typedEntries[canonical] = HashEntry{Key: entryKey, Value: entryVal}
				hd.order = append(hd.order, canonical)
			}
			slices.SortFunc(hd.order, func(a, b HashLookupKey) int {
				return cmp.Compare(a.text, b.text)
			})
		}
		canonical, err := NewHashLookupKey(key)
		if err != nil {
			return err
		}
		if _, exists := hd.typedEntries[canonical]; !exists {
			hd.order = append(hd.order, canonical)
		}
		hd.typedEntries[canonical] = HashEntry{Key: key, Value: val}
		if len(hd.typedEntries) > hd.typedEntryCapacity {
			hd.typedEntryCapacity = len(hd.typedEntries)
		}
		if hd.entries != nil {
			hd.entries[HashDisplayKey(key)] = val
		}
		return nil
	case KindObject:
		if key.kind != KindString && key.kind != KindSymbol {
			return fmt.Errorf("unsupported hash key type %s", key.kind)
		}
		BumpMutationEpoch()
		v.data.(*objectData).entries[key.String()] = val
		return nil
	default:
		return fmt.Errorf("%s is not a hash", v.kind)
	}
}

func promotedLegacyHashKey(displayKey string, incoming Value) Value {
	if (incoming.kind == KindString || incoming.kind == KindSymbol) && incoming.String() == displayKey {
		return incoming
	}
	return NewString(displayKey)
}

// HashDeleteKey removes the entry for key from a hash or object in place,
// returning the removed value and whether the key was present. On a typed hash
// it removes the key's slot from the recorded insertion order (keeping the
// order/entries invariant HashSet maintains) and mirrors the removal into the
// materialized legacy map when one exists. A missing key leaves the hash
// untouched. An error is returned only for an unsupported key.
func (v Value) HashDeleteKey(key Value) (Value, bool, error) {
	switch v.kind {
	case KindHash:
		hd := v.data.(*hashData)
		if hd.typedEntries != nil {
			canonical, err := NewHashLookupKey(key)
			if err != nil {
				return NewNil(), false, err
			}
			entry, ok := hd.typedEntries[canonical]
			if !ok {
				return NewNil(), false, nil
			}
			BumpMutationEpoch()
			delete(hd.typedEntries, canonical)
			for i, lookupKey := range hd.order {
				if lookupKey == canonical {
					hd.order = append(hd.order[:i], hd.order[i+1:]...)
					break
				}
			}
			if hd.entries != nil {
				delete(hd.entries, HashDisplayKey(entry.Key))
			}
			return entry.Value, true, nil
		}
		// Mirror HashGet's legacy lookup exactly: a bare string map only
		// resolves string and symbol keys.
		if hd.entries == nil || (key.kind != KindString && key.kind != KindSymbol) {
			return NewNil(), false, nil
		}
		val, ok := hd.entries[key.String()]
		if !ok {
			return NewNil(), false, nil
		}
		BumpMutationEpoch()
		delete(hd.entries, key.String())
		return val, true, nil
	case KindObject:
		if key.kind != KindString && key.kind != KindSymbol {
			return NewNil(), false, nil
		}
		entries := v.data.(*objectData).entries
		val, ok := entries[key.String()]
		if !ok {
			return NewNil(), false, nil
		}
		BumpMutationEpoch()
		delete(entries, key.String())
		return val, true, nil
	default:
		return NewNil(), false, nil
	}
}

// HashClearEntries removes every entry from a hash or object in place,
// preserving a hash's Ruby-style default metadata (Ruby's Hash#clear keeps the
// default). The typed-entry map, insertion order, and any materialized legacy
// map are replaced with fresh empty storage so the old backings are released.
func (v Value) HashClearEntries() {
	switch v.kind {
	case KindHash:
		hd := v.data.(*hashData)
		BumpMutationEpoch()
		if hd.typedEntries != nil {
			hd.typedEntries = make(map[HashLookupKey]HashEntry)
		}
		hd.typedEntryCapacity = 0
		hd.order = nil
		if hd.entries != nil {
			hd.entries = map[string]Value{}
		}
	case KindObject:
		BumpMutationEpoch()
		clear(v.data.(*objectData).entries)
	}
}

// forEachTypedEntry invokes fn for each typed entry, walking the recorded
// insertion order. The Go-map fallback only fires if the order record does not
// cover the map, which cannot happen through HashSet (the sole typed-entry
// writer); it keeps a partially constructed or future-variant hash renderable
// instead of panicking. fn's first error aborts the walk.
func (hd *hashData) forEachTypedEntry(fn func(HashEntry) error) error {
	if len(hd.order) == len(hd.typedEntries) {
		for _, lookupKey := range hd.order {
			if err := fn(hd.typedEntries[lookupKey]); err != nil {
				return err
			}
		}
		return nil
	}
	for _, entry := range hd.typedEntries {
		if err := fn(entry); err != nil {
			return err
		}
	}
	return nil
}

// HashOrderCapacity returns the capacity of the insertion-order backing a hash
// retains alongside its typed entries, or 0 when v is not a hash or tracks no
// order. Memory-quota accounting charges the backing's structural bytes; the
// lookup keys inside it alias strings the entry storage already charges.
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

// HashTypedEntryCapacity returns the minimum typed-entry map capacity the hash
// is known to retain. Go does not expose current map bucket capacity, so this
// tracks explicit reservations plus the live entry count reached through
// HashSet. Memory-quota estimation uses it to charge reserved typed buckets
// that may exceed len(typedEntries).
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func HashTypedEntryCapacity(v Value) int {
	if v.kind != KindHash {
		return 0
	}
	if hd, ok := v.data.(*hashData); ok && hd.typedEntries != nil {
		if len(hd.typedEntries) > hd.typedEntryCapacity {
			return len(hd.typedEntries)
		}
		return hd.typedEntryCapacity
	}
	return 0
}

// ReserveHashOrder pre-sizes the insertion-order backing to capacity n so a
// builder that knows its final entry count avoids the append growth overshoot,
// where a hash of 3 entries would otherwise retain 4 order slots. This keeps the
// backing's capacity equal to the entry count the memory-quota projection
// charges. It does not initialize typed-entry storage, so hash literals can
// reserve order capacity without allocating typed buckets before their
// per-entry accounting runs. It is a no-op when v is not a hash, n is
// non-positive, or the backing already has at least n slots.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) ReserveHashOrder(n int) {
	if v.kind != KindHash || n <= 0 {
		return
	}
	hd, ok := v.data.(*hashData)
	if !ok || cap(hd.order) >= n {
		return
	}
	BumpMutationEpoch()
	grown := make([]HashLookupKey, len(hd.order), n)
	copy(grown, hd.order)
	hd.order = grown
}

// ReserveTypedHashOrder prepares an empty hash builder for typed-key writes and
// reserves its insertion-order backing. It preserves a materialized legacy map
// when one already exists, which keeps Hash() callers synchronized as HashSet
// populates typed entries. It is a no-op for non-hashes, negative capacities, or
// legacy hashes that already contain entries.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) ReserveTypedHashOrder(n int) {
	if v.kind != KindHash || n < 0 {
		return
	}
	hd, ok := v.data.(*hashData)
	if !ok {
		return
	}
	if hd.typedEntries == nil {
		if len(hd.entries) != 0 {
			return
		}
		BumpMutationEpoch()
		hd.typedEntries = make(map[HashLookupKey]HashEntry, n)
		hd.typedEntryCapacity = n
		v.ReserveHashOrder(n)
		return
	}
	if n > hd.typedEntryCapacity {
		BumpMutationEpoch()
		grown := make(map[HashLookupKey]HashEntry, n)
		maps.Copy(grown, hd.typedEntries)
		hd.typedEntries = grown
		hd.typedEntryCapacity = n
	}
	v.ReserveHashOrder(n)
}
