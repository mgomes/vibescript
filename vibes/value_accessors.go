package vibes

import "time"

// Kind returns the ValueKind of v.
func (v Value) Kind() ValueKind { return v.kind }

// IsNil reports whether v is a nil value.
func (v Value) IsNil() bool { return v.kind == KindNil }

// Bool returns the boolean content of v, or false if v is not a bool.
func (v Value) Bool() bool {
	if v.kind == KindBool {
		return v.data.(bool)
	}
	return false
}

// Int returns the integer content of v, coercing from float if needed.
func (v Value) Int() int64 {
	switch v.kind {
	case KindInt:
		return v.data.(int64)
	case KindFloat:
		return int64(v.data.(float64))
	default:
		return 0
	}
}

// Float returns the float content of v, coercing from int if needed.
func (v Value) Float() float64 {
	switch v.kind {
	case KindFloat:
		return v.data.(float64)
	case KindInt:
		return float64(v.data.(int64))
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

// Class returns the class definition of v, or nil if v is not a class.
func (v Value) Class() *ClassDef {
	if v.kind != KindClass {
		return nil
	}
	return v.data.(*ClassDef)
}

// Instance returns the class instance of v, or nil if v is not an instance.
func (v Value) Instance() *Instance {
	if v.kind != KindInstance {
		return nil
	}
	return v.data.(*Instance)
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
	return v.data.(Duration)
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

// Function returns the script function of v, or nil if v is not a function.
func (v Value) Function() *ScriptFunction {
	if v.kind != KindFunction {
		return nil
	}
	return v.data.(*ScriptFunction)
}

// Builtin returns the builtin function of v, or nil if v is not a builtin.
func (v Value) Builtin() *Builtin {
	if v.kind != KindBuiltin {
		return nil
	}
	return v.data.(*Builtin)
}

// Block returns the block (closure) of v, or nil if v is not a block.
func (v Value) Block() *Block {
	if v.kind != KindBlock {
		return nil
	}
	return v.data.(*Block)
}

// Enum returns the enum definition of v, or nil if v is not an enum.
func (v Value) Enum() *EnumDef {
	if v.kind != KindEnum {
		return nil
	}
	return v.data.(*EnumDef)
}

// EnumValue returns the enum member of v, or nil if v is not an enum value.
func (v Value) EnumValue() *EnumValueDef {
	if v.kind != KindEnumValue {
		return nil
	}
	return v.data.(*EnumValueDef)
}
