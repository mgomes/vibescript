package vibes

import "time"

func NewNil() Value            { return Value{kind: KindNil} }
func NewBool(b bool) Value     { return Value{kind: KindBool, data: b} }
func NewInt(i int64) Value     { return Value{kind: KindInt, data: i} }
func NewFloat(f float64) Value { return Value{kind: KindFloat, data: f} }
func NewString(s string) Value { return Value{kind: KindString, data: s} }
func NewArray(a []Value) Value { return Value{kind: KindArray, data: a} }
func NewHash(h map[string]Value) Value {
	return Value{kind: KindHash, data: h}
}
func NewMoney(m Money) Value       { return Value{kind: KindMoney, data: m} }
func NewDuration(d Duration) Value { return Value{kind: KindDuration, data: d} }
func NewTime(t time.Time) Value    { return Value{kind: KindTime, data: t} }
func NewSymbol(name string) Value  { return Value{kind: KindSymbol, data: name} }
func NewObject(attrs map[string]Value) Value {
	return Value{kind: KindObject, data: attrs}
}
func NewRange(r Range) Value { return Value{kind: KindRange, data: r} }
func NewBlock(params []Param, body []Statement, env *Env) Value {
	return Value{kind: KindBlock, data: &Block{Params: params, Body: body, Env: env}}
}

func NewClass(def *ClassDef) Value     { return Value{kind: KindClass, data: def} }
func NewInstance(inst *Instance) Value { return Value{kind: KindInstance, data: inst} }

func newBuiltin(name string, fn BuiltinFunc, autoInvoke bool) Value {
	return Value{kind: KindBuiltin, data: &Builtin{Name: name, Fn: fn, AutoInvoke: autoInvoke}}
}

func NewBuiltin(name string, fn BuiltinFunc) Value {
	return newBuiltin(name, fn, false)
}

func NewAutoBuiltin(name string, fn BuiltinFunc) Value {
	return newBuiltin(name, fn, true)
}

func NewFunction(fn *ScriptFunction) Value {
	return Value{kind: KindFunction, data: fn}
}
