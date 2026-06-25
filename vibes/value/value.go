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

// Range represents an integer range. End is included unless Exclusive is true.
// It is a domain-shaped scalar that also serves as a Value payload
// (KindRange); it lives in the value package alongside Value itself
// because of that coupling. See doc.go for the rationale.
type Range struct {
	Start     int64
	End       int64
	Exclusive bool
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
	case KindHash:
		// A KindHash payload is internally a *hashData wrapper, but the public
		// payload exposed by Data is the bare entry map. Re-wrap it so that a
		// hash round-tripped through Data/NewValue stays a usable KindHash
		// rather than a value whose accessors panic on the wrong payload type.
		if h, ok := data.(map[string]Value); ok {
			return NewHash(h)
		}
	}
	return Value{kind: kind, data: data}
}

// Data returns the underlying payload stored in v. Callers are expected
// to type-assert against the payload type associated with v.Kind().
func (v Value) Data() any {
	switch v.kind {
	case KindHash:
		// Expose the public entry map rather than the internal *hashData
		// wrapper, so embedders can inspect entries and round-trip a hash
		// through Data/NewValue. Default metadata is reached via the dedicated
		// HashDefaultValue/HashDefaultProc accessors.
		return v.hashEntries()
	case KindBool:
		if v.data == nil {
			return v.Bool()
		}
	case KindInt:
		if v.data == nil {
			return v.Int()
		}
	case KindFloat:
		if v.data == nil {
			return v.Float()
		}
	case KindDuration:
		if v.data == nil {
			return v.Duration()
		}
	}
	return v.data
}
