package value

import (
	"math"
	"time"
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

// NewHash returns a hash (map) Value. A nil map is replaced with a freshly
// allocated empty map so that every hash has its own backing storage and thus a
// distinct object identity. Without this, two independently constructed empty
// hashes would share the nil map's zero pointer and wrongly report identical
// under the Ruby-style equal? predicate.
func NewHash(h map[string]Value) Value {
	if h == nil {
		h = map[string]Value{}
	}
	return Value{kind: KindHash, data: h}
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
// backing storage and thus a distinct object identity, matching NewHash.
func NewObject(attrs map[string]Value) Value {
	if attrs == nil {
		attrs = map[string]Value{}
	}
	return Value{kind: KindObject, data: attrs}
}

// NewRange returns a range Value.
func NewRange(r Range) Value { return Value{kind: KindRange, data: r} }
