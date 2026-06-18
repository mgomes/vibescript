package value

// ValueKind identifies the type of a runtime Value.
type ValueKind int

const (
	// KindNil is the nil value kind.
	KindNil ValueKind = iota
	KindBool
	KindInt
	KindFloat
	KindString
	KindArray
	KindHash
	KindFunction
	KindBuiltin
	KindMoney
	KindDuration
	KindTime
	KindSymbol
	KindObject
	KindRange
	KindBlock
	KindEnum
	KindEnumValue
	KindClass
	KindInstance
)

// Value is a tagged union holding any Vibescript runtime value.
type Value struct {
	kind   ValueKind
	data   any
	scalar uint64
}

// Range represents an integer range with inclusive start and end.
// It is a domain-shaped scalar that also serves as a Value payload
// (KindRange); it lives in the value package alongside Value itself
// because of that coupling. See doc.go for the rationale.
type Range struct {
	Start int64
	End   int64
}

// NewValue constructs a Value with the given kind and underlying data.
// It is intended for use by the vibes package when wrapping runtime
// payloads (blocks, classes, instances, enums, functions, builtins)
// whose types live outside this package.
func NewValue(kind ValueKind, data any) Value {
	switch kind {
	case KindBool:
		if b, ok := data.(bool); ok {
			return NewBool(b)
		}
	case KindInt:
		if i, ok := data.(int64); ok {
			return NewInt(i)
		}
	case KindFloat:
		if f, ok := data.(float64); ok {
			return NewFloat(f)
		}
	case KindDuration:
		if d, ok := data.(Duration); ok {
			return NewDuration(d)
		}
	}
	return Value{kind: kind, data: data}
}

// Data returns the underlying payload stored in v. Callers are expected
// to type-assert against the payload type associated with v.Kind().
func (v Value) Data() any {
	if v.data != nil {
		return v.data
	}
	switch v.kind {
	case KindBool:
		return v.Bool()
	case KindInt:
		return v.Int()
	case KindFloat:
		return v.Float()
	case KindDuration:
		return v.Duration()
	default:
		return nil
	}
}
