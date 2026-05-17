package value

// Runtime-kind payload interfaces.
//
// Values whose payload type lives in the runtime (block, class,
// instance, enum, function, builtin) need a way to surface a typed
// accessor on Value without forming an import cycle. Each interface
// below is implemented by the corresponding concrete type defined in
// the internal runtime; the typed accessors on Value return the
// interface, and the runtime keeps the concrete type private. Embedders
// observe these payloads only through the interfaces.
//
// The marker methods are intentionally minimal: they exist only to bind
// the interface to a specific concrete type, not to expose behavior.

// ClassPayload is the marker implemented by the runtime class type so
// Value.Class can return a typed result without importing the runtime.
type ClassPayload interface{ ValueClassMarker() }

// InstancePayload is the marker implemented by the runtime instance type.
type InstancePayload interface{ ValueInstanceMarker() }

// BlockPayload is the marker implemented by the runtime block type.
type BlockPayload interface{ ValueBlockMarker() }

// FunctionPayload is the marker implemented by the runtime script-function type.
type FunctionPayload interface{ ValueFunctionMarker() }

// BuiltinPayload is the marker implemented by the runtime builtin type.
type BuiltinPayload interface{ ValueBuiltinMarker() }

// EnumPayload is the marker implemented by the runtime enum type.
type EnumPayload interface{ ValueEnumMarker() }

// EnumValuePayload is the marker implemented by the runtime enum-value type.
type EnumValuePayload interface{ ValueEnumValueMarker() }
