package vibes

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

// valueClass returns the *ClassDef stored in v, or nil.
func valueClass(v Value) *ClassDef {
	cl, _ := v.Class().(*ClassDef)
	return cl
}

// valueInstance returns the *Instance stored in v, or nil.
func valueInstance(v Value) *Instance {
	inst, _ := v.Instance().(*Instance)
	return inst
}

// valueBlock returns the *Block stored in v, or nil.
func valueBlock(v Value) *Block {
	blk, _ := v.Block().(*Block)
	return blk
}

// valueFunction returns the *ScriptFunction stored in v, or nil.
func valueFunction(v Value) *ScriptFunction {
	fn, _ := v.Function().(*ScriptFunction)
	return fn
}

// valueBuiltin returns the *Builtin stored in v, or nil.
func valueBuiltin(v Value) *Builtin {
	b, _ := v.Builtin().(*Builtin)
	return b
}

// valueEnum returns the *EnumDef stored in v, or nil.
func valueEnum(v Value) *EnumDef {
	e, _ := v.Enum().(*EnumDef)
	return e
}

// valueEnumValue returns the *EnumValueDef stored in v, or nil.
func valueEnumValue(v Value) *EnumValueDef {
	e, _ := v.EnumValue().(*EnumValueDef)
	return e
}

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
