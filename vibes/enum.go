package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// EnumDef represents a user-defined enumeration with named members.
type EnumDef = runtime.EnumDef

// EnumValueDef represents a single member within an EnumDef.
type EnumValueDef = runtime.EnumValueDef

// NewEnum returns an enum definition Value.
func NewEnum(def *EnumDef) Value { return runtime.NewEnum(def) }

// NewEnumValue returns an enum member Value.
func NewEnumValue(def *EnumValueDef) Value { return runtime.NewEnumValue(def) }

// EnumOf returns the *EnumDef stored in v, or nil.
func EnumOf(v Value) *EnumDef { return runtime.EnumOf(v) }

// EnumValueOf returns the *EnumValueDef stored in v, or nil.
func EnumValueOf(v Value) *EnumValueDef { return runtime.EnumValueOf(v) }
