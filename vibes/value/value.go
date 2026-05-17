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
	kind ValueKind
	data any
}

// Range represents an integer range with inclusive start and end.
type Range struct {
	Start int64
	End   int64
}

// NewValue constructs a Value with the given kind and underlying data.
// It is intended for use by the vibes package when wrapping runtime
// payloads (blocks, classes, instances, enums, functions, builtins)
// whose types live outside this package.
func NewValue(kind ValueKind, data any) Value {
	return Value{kind: kind, data: data}
}

// Data returns the underlying payload stored in v. Callers are expected
// to type-assert against the payload type associated with v.Kind().
func (v Value) Data() any { return v.data }
