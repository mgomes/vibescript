package vibes

import (
	"time"

	"github.com/mgomes/vibescript/vibes/value"
)

// Type aliases for types that have moved to vibes/value. These keep the
// vibes package source-compatible with external embedders and with the
// rest of vibes during the transition. They will be removed in v0.29.0
// once direct vibes/value imports replace these forwarders.
//
// Known intentional break in this carve: runtime-bound accessors on
// Value (Builtin, Class, Instance, Function, Block, Enum, EnumValue)
// now return marker-interface payloads defined in value (e.g.
// value.BuiltinPayload) instead of the concrete *vibes.Builtin etc.
// The concrete types live in this package and would create an import
// cycle if value referenced them directly, and Go does not allow
// methods on aliased types to be redefined in the alias package. Use
// the typed companions in value_runtime.go (BuiltinOf, ClassOf,
// InstanceOf, FunctionOf, EnumValueOf) for direct access, or
// type-assert against the concrete *vibes.* types. Data-only
// accessors (Bool, Int, Float, String, Array, Hash, Money, Duration,
// Time, Range) keep their original signatures unchanged.
type (
	Value     = value.Value
	ValueKind = value.ValueKind
	Money     = value.Money
	Duration  = value.Duration
	Range     = value.Range
)

// Kind constants re-exported from vibes/value.
const (
	KindNil       = value.KindNil
	KindBool      = value.KindBool
	KindInt       = value.KindInt
	KindFloat     = value.KindFloat
	KindString    = value.KindString
	KindArray     = value.KindArray
	KindHash      = value.KindHash
	KindFunction  = value.KindFunction
	KindBuiltin   = value.KindBuiltin
	KindMoney     = value.KindMoney
	KindDuration  = value.KindDuration
	KindTime      = value.KindTime
	KindSymbol    = value.KindSymbol
	KindObject    = value.KindObject
	KindRange     = value.KindRange
	KindBlock     = value.KindBlock
	KindEnum      = value.KindEnum
	KindEnumValue = value.KindEnumValue
	KindClass     = value.KindClass
	KindInstance  = value.KindInstance
)

// Constructors are exposed as thin wrapper functions rather than vars so
// the public API stays immutable for embedders.

// NewNil returns a nil Value.
func NewNil() Value { return value.NewNil() }

// NewBool returns a boolean Value.
func NewBool(b bool) Value { return value.NewBool(b) }

// NewInt returns an integer Value.
func NewInt(i int64) Value { return value.NewInt(i) }

// NewFloat returns a floating-point Value.
func NewFloat(f float64) Value { return value.NewFloat(f) }

// NewString returns a string Value.
func NewString(s string) Value { return value.NewString(s) }

// NewArray returns an array Value.
func NewArray(a []Value) Value { return value.NewArray(a) }

// NewHash returns a hash (map) Value.
func NewHash(h map[string]Value) Value { return value.NewHash(h) }

// NewSymbol returns a symbol Value.
func NewSymbol(name string) Value { return value.NewSymbol(name) }

// NewObject returns an object Value with the given attributes.
func NewObject(attrs map[string]Value) Value { return value.NewObject(attrs) }

// NewMoney returns a money Value.
func NewMoney(m Money) Value { return value.NewMoney(m) }

// NewDuration returns a duration Value.
func NewDuration(d Duration) Value { return value.NewDuration(d) }

// NewTime returns a time Value.
func NewTime(t time.Time) Value { return value.NewTime(t) }

// NewRange returns a range Value.
func NewRange(r Range) Value { return value.NewRange(r) }
