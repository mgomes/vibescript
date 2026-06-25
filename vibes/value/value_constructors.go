package value

import (
	"math"
	"reflect"
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

// NewArray returns an array Value.
func NewArray(a []Value) Value { return Value{kind: KindArray, data: a} }

// hashData backs a KindHash value. It pairs the entry map with optional
// Ruby-style default metadata consulted on missing-key lookup: either a default
// value (returned without inserting) or a default proc (a KindBlock value the
// runtime invokes with the hash and key). KindObject keeps a bare map because
// objects never carry hash defaults.
type hashData struct {
	entries      map[string]Value
	defaultValue Value
	defaultProc  Value
}

// HashDataBytes is the heap footprint of the hashData wrapper every KindHash
// value allocates, excluding the entry map and default payloads it points at.
// Memory-quota accounting charges it once per distinct hash so an array of many
// small hashes cannot retain the per-hash wrapper cost uncharged.
const HashDataBytes = int(unsafe.Sizeof(hashData{}))

// NewHash returns a hash (map) Value with no default.
func NewHash(h map[string]Value) Value {
	return Value{kind: KindHash, data: &hashData{entries: h}}
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
func (v Value) SetHashDefaults(defaultValue, defaultProc Value) {
	if v.kind != KindHash {
		return
	}
	if hd, ok := v.data.(*hashData); ok {
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

// HashIdentity returns a stable identity for a hash wrapper, or 0 when v is not
// a hash. Unlike the entry-map pointer, this identifies the whole hashData
// wrapper, so two KindHash values that share an entry map but carry different
// default metadata are distinct. Cycle-detecting scanners that must also visit
// hash defaults key their seen-set on this value rather than the bare entry map,
// which would otherwise hide a second wrapper's distinct default payload.
func HashIdentity(v Value) uintptr {
	if v.kind != KindHash {
		return 0
	}
	if hd, ok := v.data.(*hashData); ok {
		return reflect.ValueOf(hd).Pointer()
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
	return Value{kind: KindObject, data: attrs}
}

// NewRange returns a range Value.
func NewRange(r Range) Value { return Value{kind: KindRange, data: r} }
