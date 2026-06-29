package value

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// HashEntry is one hash entry with the original script key preserved.
type HashEntry struct {
	Key   Value
	Value Value
}

// HashLookupKey is a comparable hash-key identity used for hash table lookups.
// It preserves Ruby-style key identity without materializing canonical strings
// for scalar keys on hot paths.
type HashLookupKey struct {
	kind           ValueKind
	text           string
	number         int64
	bits           uint64
	flag           bool
	rangeEnd       int64
	rangeExclusive bool
}

// NewHashLookupKey returns the comparable lookup key for a hash key value.
func NewHashLookupKey(key Value) (HashLookupKey, error) {
	switch key.kind {
	case KindNil:
		return HashLookupKey{kind: KindNil}, nil
	case KindBool:
		return HashLookupKey{kind: KindBool, flag: key.Bool()}, nil
	case KindInt:
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

// HashKey returns the canonical lookup key for a hash key value.
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
		return "range:" + strconv.FormatInt(r.Start, 10) + ":" + strconv.FormatInt(r.End, 10) + ":" + strconv.FormatBool(r.Exclusive), nil
	default:
		return "", fmt.Errorf("unsupported hash key type %s", key.kind)
	}
}

func encodeHashKeyString(s string) string {
	return strconv.Itoa(len(s)) + ":" + s
}

// HashDisplayKey returns the legacy string-map key used by Hash() for callers
// that inspect ordinary hashes through the public map API.
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
		return len(v.data.(map[string]Value))
	default:
		return 0
	}
}

// HashHasTypedEntries reports whether a hash carries canonical typed-key
// entries in addition to the legacy string-key map exposed by Hash().
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
// when it has enough capacity.
func (v Value) HashEntriesInto(buf []HashEntry) []HashEntry {
	switch v.kind {
	case KindHash:
		hd := v.data.(*hashData)
		if hd.typedEntries != nil {
			entries := buf[:0]
			if cap(entries) < len(hd.typedEntries) {
				entries = make([]HashEntry, 0, len(hd.typedEntries))
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
		obj := v.data.(map[string]Value)
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
		val, ok := v.data.(map[string]Value)[key.String()]
		return val, ok, nil
	default:
		return NewNil(), false, nil
	}
}

// HashSet stores key/value in a hash or object.
func (v Value) HashSet(key, val Value) error {
	switch v.kind {
	case KindHash:
		hd := v.data.(*hashData)
		if hd.typedEntries == nil {
			if hd.entries == nil {
				hd.entries = make(map[string]Value)
			}
			hd.typedEntries = make(map[HashLookupKey]HashEntry)
			for displayKey, value := range hd.entries {
				entryKey := promotedLegacyHashKey(displayKey, key)
				canonical, err := NewHashLookupKey(entryKey)
				if err != nil {
					return err
				}
				hd.typedEntries[canonical] = HashEntry{Key: entryKey, Value: value}
			}
		}
		canonical, err := NewHashLookupKey(key)
		if err != nil {
			return err
		}
		hd.typedEntries[canonical] = HashEntry{Key: key, Value: val}
		if hd.entries != nil {
			hd.entries[HashDisplayKey(key)] = val
		}
		return nil
	case KindObject:
		if key.kind != KindString && key.kind != KindSymbol {
			return fmt.Errorf("unsupported hash key type %s", key.kind)
		}
		v.data.(map[string]Value)[key.String()] = val
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
