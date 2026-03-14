package vibes

import "time"

// NewNil returns a nil Value.
func NewNil() Value { return Value{kind: KindNil} }

// NewBool returns a boolean Value.
func NewBool(b bool) Value { return Value{kind: KindBool, data: b} }

// NewInt returns an integer Value.
func NewInt(i int64) Value { return Value{kind: KindInt, data: i} }

// NewFloat returns a floating-point Value.
func NewFloat(f float64) Value { return Value{kind: KindFloat, data: f} }

// NewString returns a string Value.
func NewString(s string) Value { return Value{kind: KindString, data: s} }

// NewArray returns an array Value.
func NewArray(a []Value) Value { return Value{kind: KindArray, data: a} }
// NewHash returns a hash (map) Value.
func NewHash(h map[string]Value) Value {
	return Value{kind: KindHash, data: h}
}
// NewMoney returns a money Value.
func NewMoney(m Money) Value { return Value{kind: KindMoney, data: m} }

// NewDuration returns a duration Value.
func NewDuration(d Duration) Value { return Value{kind: KindDuration, data: d} }

// NewTime returns a time Value.
func NewTime(t time.Time) Value { return Value{kind: KindTime, data: t} }

// NewSymbol returns a symbol Value.
func NewSymbol(name string) Value { return Value{kind: KindSymbol, data: name} }
// NewObject returns an object Value with the given attributes.
func NewObject(attrs map[string]Value) Value {
	return Value{kind: KindObject, data: attrs}
}
// NewRange returns a range Value.
func NewRange(r Range) Value { return Value{kind: KindRange, data: r} }
// NewBlock returns a block (closure) Value.
func NewBlock(params []Param, body []Statement, env *Env) Value {
	return Value{kind: KindBlock, data: &Block{Params: params, Body: body, Env: env}}
}

// NewEnum returns an enum definition Value.
func NewEnum(def *EnumDef) Value { return Value{kind: KindEnum, data: def} }

// NewEnumValue returns an enum member Value.
func NewEnumValue(def *EnumValueDef) Value { return Value{kind: KindEnumValue, data: def} }

// NewClass returns a class definition Value.
func NewClass(def *ClassDef) Value { return Value{kind: KindClass, data: def} }

// NewInstance returns a class instance Value.
func NewInstance(inst *Instance) Value { return Value{kind: KindInstance, data: inst} }

func newBuiltin(name string, fn BuiltinFunc, autoInvoke bool) Value {
	return Value{kind: KindBuiltin, data: &Builtin{Name: name, Fn: fn, AutoInvoke: autoInvoke}}
}

// NewBuiltin returns a builtin function Value.
func NewBuiltin(name string, fn BuiltinFunc) Value {
	return newBuiltin(name, fn, false)
}

// NewAutoBuiltin returns a builtin function Value that auto-invokes without parentheses.
func NewAutoBuiltin(name string, fn BuiltinFunc) Value {
	return newBuiltin(name, fn, true)
}

// NewFunction returns a script-defined function Value.
func NewFunction(fn *ScriptFunction) Value {
	return Value{kind: KindFunction, data: fn}
}
