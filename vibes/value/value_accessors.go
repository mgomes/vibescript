package value

import (
	"math"
	"time"
)

// Kind returns the ValueKind of v.
func (v Value) Kind() ValueKind { return v.kind }

// IsNil reports whether v is a nil value.
func (v Value) IsNil() bool { return v.kind == KindNil }

// Bool returns the boolean content of v, or false if v is not a bool.
func (v Value) Bool() bool {
	if v.kind == KindBool {
		if v.data == nil {
			return v.scalar != 0
		}
		if b, ok := v.data.(bool); ok {
			return b
		}
	}
	return false
}

// Int returns the integer content of v, coercing from float if needed.
func (v Value) Int() int64 {
	switch v.kind {
	case KindInt:
		if v.data == nil {
			return int64(v.scalar)
		}
		if i, ok := v.data.(int64); ok {
			return i
		}
		return 0
	case KindFloat:
		return int64(v.Float())
	default:
		return 0
	}
}

// Float returns the float content of v, coercing from int if needed.
func (v Value) Float() float64 {
	switch v.kind {
	case KindFloat:
		if v.data == nil {
			return math.Float64frombits(v.scalar)
		}
		if f, ok := v.data.(float64); ok {
			return f
		}
		return 0
	case KindInt:
		return float64(v.Int())
	default:
		return 0
	}
}

// Array returns the array content of v, or nil if v is not an array.
func (v Value) Array() []Value {
	if v.kind != KindArray {
		return nil
	}
	return v.data.([]Value)
}

// Hash returns the hash content of v, or nil if v is not a hash or object.
func (v Value) Hash() map[string]Value {
	if v.kind != KindHash && v.kind != KindObject {
		return nil
	}
	return v.data.(map[string]Value)
}

// Money returns the money content of v, or a zero Money if v is not money.
func (v Value) Money() Money {
	if v.kind != KindMoney {
		return Money{}
	}
	return v.data.(Money)
}

// Duration returns the duration content of v, or a zero Duration if v is not a duration.
func (v Value) Duration() Duration {
	if v.kind != KindDuration {
		return Duration{}
	}
	if v.data == nil {
		return DurationFromSeconds(int64(v.scalar))
	}
	if d, ok := v.data.(Duration); ok {
		return d
	}
	return Duration{}
}

// Time returns the time content of v, or a zero time if v is not a time.
func (v Value) Time() time.Time {
	if v.kind != KindTime {
		return time.Time{}
	}
	return v.data.(time.Time)
}

// Range returns the range content of v, or a zero Range if v is not a range.
func (v Value) Range() Range {
	if v.kind != KindRange {
		return Range{}
	}
	return v.data.(Range)
}

// Class returns the underlying class payload of v, or nil if v is not a
// class. The concrete type is private to the runtime; callers operate
// through the ClassPayload marker.
func (v Value) Class() ClassPayload {
	if v.kind != KindClass {
		return nil
	}
	cl, _ := v.data.(ClassPayload)
	return cl
}

// Instance returns the underlying instance payload of v, or nil if v is
// not an instance. The concrete type is private to the runtime; callers
// operate through the InstancePayload marker.
func (v Value) Instance() InstancePayload {
	if v.kind != KindInstance {
		return nil
	}
	inst, _ := v.data.(InstancePayload)
	return inst
}

// Block returns the underlying block payload of v, or nil if v is not a
// block. The concrete type is private to the runtime; callers operate
// through the BlockPayload marker.
func (v Value) Block() BlockPayload {
	if v.kind != KindBlock {
		return nil
	}
	blk, _ := v.data.(BlockPayload)
	return blk
}

// Function returns the underlying script-function payload of v, or nil if
// v is not a function. The concrete type is private to the runtime;
// callers operate through the FunctionPayload marker.
func (v Value) Function() FunctionPayload {
	if v.kind != KindFunction {
		return nil
	}
	fn, _ := v.data.(FunctionPayload)
	return fn
}

// Builtin returns the underlying builtin payload of v, or nil if v is not
// a builtin. The concrete type is private to the runtime; callers
// operate through the BuiltinPayload marker.
func (v Value) Builtin() BuiltinPayload {
	if v.kind != KindBuiltin {
		return nil
	}
	b, _ := v.data.(BuiltinPayload)
	return b
}

// Enum returns the underlying enum definition payload of v, or nil if v
// is not an enum. The concrete type is private to the runtime; callers
// operate through the EnumPayload marker.
func (v Value) Enum() EnumPayload {
	if v.kind != KindEnum {
		return nil
	}
	e, _ := v.data.(EnumPayload)
	return e
}

// EnumValue returns the underlying enum value payload of v, or nil if v
// is not an enum value. The concrete type is private to the runtime;
// callers operate through the EnumValuePayload marker.
func (v Value) EnumValue() EnumValuePayload {
	if v.kind != KindEnumValue {
		return nil
	}
	e, _ := v.data.(EnumValuePayload)
	return e
}
