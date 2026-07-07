package value

import (
	"math/big"
	"strconv"
)

// ValueKind identifies the type of a runtime Value.
type ValueKind int

const (
	// KindNil is the nil value kind.
	KindNil ValueKind = iota
	// KindBool tags a boolean payload (Bool).
	KindBool
	// KindInt tags a 64-bit integer payload (Int).
	KindInt
	// KindFloat tags a 64-bit floating-point payload (Float).
	KindFloat
	// KindString tags a string payload (String).
	KindString
	// KindArray tags a mutable array payload (Array). The payload is a
	// shared wrapper, so Value copies alias the same elements.
	KindArray
	// KindHash tags a mutable hash payload (Hash, HashGet, HashSet, ...).
	// Like arrays, the payload is a shared wrapper.
	KindHash
	// KindFunction tags a script-defined function (Function). The concrete
	// payload type lives in the runtime.
	KindFunction
	// KindBuiltin tags a Go-implemented builtin function (Builtin).
	KindBuiltin
	// KindMoney tags a Money payload (Money).
	KindMoney
	// KindDuration tags a Duration payload (Duration).
	KindDuration
	// KindTime tags a time.Time payload (Time).
	KindTime
	// KindSymbol tags a Ruby-style symbol; String returns its name without
	// the leading colon.
	KindSymbol
	// KindObject tags a string-keyed attribute bag (Hash exposes the map).
	// Unlike KindHash it never carries default metadata.
	KindObject
	// KindRange tags a Range payload (Range).
	KindRange
	// KindBlock tags a script block (Block). The concrete payload type
	// lives in the runtime.
	KindBlock
	// KindEnum tags an enum definition (Enum). The concrete payload type
	// lives in the runtime.
	KindEnum
	// KindEnumValue tags one member of an enum (EnumValue). The concrete
	// payload type lives in the runtime.
	KindEnumValue
	// KindClass tags a script-defined class (Class). The concrete payload
	// type lives in the runtime.
	KindClass
	// KindInstance tags an instance of a script-defined class (Instance).
	// The concrete payload type lives in the runtime.
	KindInstance
	// KindRegex tags a compiled regex literal payload (Regex).
	KindRegex
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
	// Beginless and Endless mark Ruby's open-ended ranges (..n and n..).
	// The corresponding endpoint field is meaningless when its flag is set;
	// both false is an ordinary bounded range, so existing constructors are
	// unaffected. Both true is never produced (a bare .. is a parse error).
	Beginless bool
	Endless   bool
}

// NewValue constructs a Value with the given kind and underlying data.
// It is intended for use by the vibes package when wrapping runtime
// payloads (blocks, classes, instances, enums, functions, builtins)
// whose types live outside this package. Hosts should use the typed
// constructors (NewInt, NewArray, ...); NewValue carries no compatibility
// promise for payload shapes (see docs/embedding-api-stability.md).
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
		// A big-integer payload round-tripped through Data/NewValue is copied
		// and re-normalized so the canonical invariant (big payloads never fit
		// int64) holds even for hosts that mutate the payload between calls.
		if bi, ok := data.(*big.Int); ok {
			return NewBigInt(bi)
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
	case KindArray:
		// A KindArray payload is internally a *arrayData wrapper, but the public
		// payload exposed by Data is the bare element slice. Re-wrap it so that
		// an array round-tripped through Data/NewValue stays a usable KindArray.
		if a, ok := data.([]Value); ok {
			return NewArray(a)
		}
	}
	return Value{kind: kind, data: data}
}

// Data returns the underlying payload stored in v. Callers are expected
// to type-assert against the payload type associated with v.Kind().
// Prefer the typed accessors (Int, Array, Hash, ...): they cover every kind,
// and for the runtime-only kinds (functions, blocks, classes, instances,
// enums) the concrete payload type behind Data is internal to the interpreter
// and carries no compatibility promise.
//
// An integer outside the int64 range exposes its live *big.Int payload, like
// arrays and hashes expose their live backing. Mutating it corrupts the value
// (big payloads are immutable by contract); callers that need an owned copy
// use BigInt instead.
func (v Value) Data() any {
	switch v.kind {
	case KindHash:
		// Expose the public entry map rather than the internal *hashData
		// wrapper, so embedders can inspect entries and round-trip a hash
		// through Data/NewValue. Default metadata is reached via the dedicated
		// HashDefaultValue/HashDefaultProc accessors.
		return v.Hash()
	case KindArray:
		// Expose the public element slice rather than the internal *arrayData
		// wrapper, so embedders can inspect elements and round-trip an array
		// through Data/NewValue.
		return v.Array()
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

// String renders the range the way it is written in source, including the
// open-ended forms: 1..5, 1..., ..5, 1.., and so on.
func (r Range) String() string {
	dots := ".."
	if r.Exclusive {
		dots = "..."
	}
	start, end := "", ""
	if !r.Beginless {
		start = strconv.FormatInt(r.Start, 10)
	}
	if !r.Endless {
		end = strconv.FormatInt(r.End, 10)
	}
	return start + dots + end
}
