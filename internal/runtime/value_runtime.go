package runtime

import (
	"fmt"

	"github.com/mgomes/vibescript/vibes/value"
)

// Builtin represents a built-in function callable from Vibescript. It
// remains defined in the vibes package because BuiltinFunc references
// the runtime *Execution type.
type Builtin struct {
	Name       string
	Fn         BuiltinFunc
	AutoInvoke bool
}

// BuiltinFunc is the Go function signature for built-in Vibescript functions.
type BuiltinFunc func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error)

// Block represents a closure passed to a function at runtime. It stays
// in the vibes package because its fields reference parser AST and the
// runtime Env/Script types.
type Block struct {
	Params     []Param
	Body       []Statement
	Env        *Env
	owner      *Script
	moduleKey  string
	modulePath string
	moduleRoot string
}

// NewBlock returns a block (closure) Value.
func NewBlock(params []Param, body []Statement, env *Env) Value {
	return value.NewValue(KindBlock, &Block{Params: params, Body: body, Env: env})
}

// NewEnum returns an enum definition Value.
func NewEnum(def *EnumDef) Value { return value.NewValue(KindEnum, def) }

// NewEnumValue returns an enum member Value.
func NewEnumValue(def *EnumValueDef) Value { return value.NewValue(KindEnumValue, def) }

// NewClass returns a class definition Value.
func NewClass(def *ClassDef) Value { return value.NewValue(KindClass, def) }

// NewInstance returns a class instance Value.
func NewInstance(inst *Instance) Value { return value.NewValue(KindInstance, inst) }

// NewFunction returns a script-defined function Value.
func NewFunction(fn *ScriptFunction) Value { return value.NewValue(KindFunction, fn) }

func newBuiltin(name string, fn BuiltinFunc, autoInvoke bool) Value {
	return value.NewValue(KindBuiltin, &Builtin{Name: name, Fn: fn, AutoInvoke: autoInvoke})
}

// NewBuiltin returns a builtin function Value.
func NewBuiltin(name string, fn BuiltinFunc) Value { return newBuiltin(name, fn, false) }

// NewAutoBuiltin returns a builtin function Value that auto-invokes without parentheses.
func NewAutoBuiltin(name string, fn BuiltinFunc) Value { return newBuiltin(name, fn, true) }

// Marker methods bind the runtime payload types to the value.* payload
// interfaces so Value.Class, Value.Builtin, and so on return a typed
// result without forming an import cycle. The names are exported so the
// marker satisfies the interfaces from another package.

func (*Builtin) ValueBuiltinMarker()         {}
func (*Block) ValueBlockMarker()             {}
func (*ClassDef) ValueClassMarker()          {}
func (*Instance) ValueInstanceMarker()       {}
func (*ScriptFunction) ValueFunctionMarker() {}
func (*EnumDef) ValueEnumMarker()            {}
func (*EnumValueDef) ValueEnumValueMarker()  {}

// ClassOf returns the *ClassDef stored in v, or nil if v is not a class
// value. It is the typed companion to v.Class(), which returns the
// value.ClassPayload interface for cycle-free reach from outside vibes.
func ClassOf(v Value) *ClassDef {
	cl, _ := v.Class().(*ClassDef)
	return cl
}

// InstanceOf returns the *Instance stored in v, or nil.
func InstanceOf(v Value) *Instance {
	inst, _ := v.Instance().(*Instance)
	return inst
}

// BlockOf returns the *Block stored in v, or nil.
func BlockOf(v Value) *Block {
	blk, _ := v.Block().(*Block)
	return blk
}

// FunctionOf returns the *ScriptFunction stored in v, or nil.
func FunctionOf(v Value) *ScriptFunction {
	fn, _ := v.Function().(*ScriptFunction)
	return fn
}

// BuiltinOf returns the *Builtin stored in v, or nil.
func BuiltinOf(v Value) *Builtin {
	b, _ := v.Builtin().(*Builtin)
	return b
}

// EnumOf returns the *EnumDef stored in v, or nil.
func EnumOf(v Value) *EnumDef {
	e, _ := v.Enum().(*EnumDef)
	return e
}

// EnumValueOf returns the *EnumValueDef stored in v, or nil.
func EnumValueOf(v Value) *EnumValueDef {
	e, _ := v.EnumValue().(*EnumValueDef)
	return e
}

// The valueX helpers preserve the original short call sites used inside
// the vibes package; new external callers should prefer the exported
// XOf functions above.
func valueClass(v Value) *ClassDef          { return ClassOf(v) }
func valueInstance(v Value) *Instance       { return InstanceOf(v) }
func valueBlock(v Value) *Block             { return BlockOf(v) }
func valueFunction(v Value) *ScriptFunction { return FunctionOf(v) }
func valueBuiltin(v Value) *Builtin         { return BuiltinOf(v) }
func valueEnum(v Value) *EnumDef            { return EnumOf(v) }
func valueEnumValue(v Value) *EnumValueDef  { return EnumValueOf(v) }

// runtimeValueString renders runtime-only value kinds whose payloads live
// in the vibes package. Installed at init time on value.RuntimeStringer.
func runtimeValueString(v Value) (string, bool) {
	switch v.Kind() {
	case KindEnum:
		if enum := valueEnum(v); enum != nil {
			return fmt.Sprintf("<Enum %s>", enum.Name), true
		}
	case KindEnumValue:
		if member := valueEnumValue(v); member != nil && member.Enum != nil {
			return fmt.Sprintf("%s::%s", member.Enum.Name, member.Name), true
		}
	case KindClass:
		if cl := valueClass(v); cl != nil {
			return fmt.Sprintf("<Class %s>", cl.Name), true
		}
	case KindInstance:
		if inst := valueInstance(v); inst != nil && inst.Class != nil {
			return fmt.Sprintf("<%s instance>", inst.Class.Name), true
		}
	}
	return "", false
}

// runtimeValueEqual compares runtime-only value kinds whose payloads live
// in the vibes package. Installed at init time on value.RuntimeEqualer.
func runtimeValueEqual(left, right Value) (bool, bool) {
	switch left.Kind() {
	case KindFunction:
		return valueFunction(left) == valueFunction(right), true
	case KindBuiltin:
		return valueBuiltin(left) == valueBuiltin(right), true
	case KindBlock:
		return valueBlock(left) == valueBlock(right), true
	case KindClass:
		return valueClass(left) == valueClass(right), true
	case KindInstance:
		return valueInstance(left) == valueInstance(right), true
	case KindEnum:
		return enumDefsEqual(valueEnum(left), valueEnum(right)), true
	case KindEnumValue:
		return enumValueDefsEqual(valueEnumValue(left), valueEnumValue(right)), true
	}
	return false, false
}

func init() {
	value.RuntimeStringer = runtimeValueString
	value.RuntimeEqualer = runtimeValueEqual
}
