package value

// Runtime-kind payload interfaces.
//
// Values whose payload type lives in the vibes package (block, class,
// instance, enum, function, builtin) need a way to surface a typed
// accessor on Value without forming an import cycle. Each interface
// below is implemented by the corresponding concrete type in the vibes
// package; the typed accessors on Value return the interface, and
// embedders type-assert to the concrete *vibes.X to reach all fields.
//
// The marker methods are intentionally minimal: they exist only to bind
// the interface to a specific concrete type, not to expose behavior.

// ClassPayload is implemented by *vibes.ClassDef so that Value.Class can
// return a typed result without importing vibes.
type ClassPayload interface{ ValueClassMarker() }

// InstancePayload is implemented by *vibes.Instance.
type InstancePayload interface{ ValueInstanceMarker() }

// BlockPayload is implemented by *vibes.Block.
type BlockPayload interface{ ValueBlockMarker() }

// FunctionPayload is implemented by *vibes.ScriptFunction.
type FunctionPayload interface{ ValueFunctionMarker() }

// BuiltinPayload is implemented by *vibes.Builtin.
type BuiltinPayload interface{ ValueBuiltinMarker() }

// EnumPayload is implemented by *vibes.EnumDef.
type EnumPayload interface{ ValueEnumMarker() }

// EnumValuePayload is implemented by *vibes.EnumValueDef.
type EnumValuePayload interface{ ValueEnumValueMarker() }
