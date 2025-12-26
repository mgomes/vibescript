package vibes

import (
	"fmt"
	"strings"
	"time"
)

type ValueKind int

const (
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
	KindClass
	KindInstance
)

func (k ValueKind) String() string {
	switch k {
	case KindNil:
		return "nil"
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindString:
		return "string"
	case KindArray:
		return "array"
	case KindHash:
		return "hash"
	case KindFunction:
		return "function"
	case KindBuiltin:
		return "builtin"
	case KindMoney:
		return "money"
	case KindDuration:
		return "duration"
	case KindTime:
		return "time"
	case KindSymbol:
		return "symbol"
	case KindObject:
		return "object"
	case KindRange:
		return "range"
	case KindBlock:
		return "block"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

type Value struct {
	kind ValueKind
	data any
}

func (v Value) Kind() ValueKind { return v.kind }

func (v Value) IsNil() bool { return v.kind == KindNil }

func (v Value) Bool() bool {
	if v.kind == KindBool {
		return v.data.(bool)
	}
	return false
}

func (v Value) Int() int64 {
	switch v.kind {
	case KindInt:
		return v.data.(int64)
	case KindFloat:
		return int64(v.data.(float64))
	default:
		return 0
	}
}

func (v Value) Float() float64 {
	switch v.kind {
	case KindFloat:
		return v.data.(float64)
	case KindInt:
		return float64(v.data.(int64))
	default:
		return 0
	}
}

func (v Value) String() string {
	switch v.kind {
	case KindString:
		return v.data.(string)
	case KindNil:
		return ""
	case KindBool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case KindInt:
		return fmt.Sprintf("%d", v.data.(int64))
	case KindFloat:
		return fmt.Sprintf("%g", v.data.(float64))
	case KindSymbol:
		return v.data.(string)
	case KindMoney:
		return v.data.(Money).String()
	case KindDuration:
		return v.data.(Duration).String()
	case KindTime:
		return v.data.(time.Time).Format(time.RFC3339Nano)
	case KindArray:
		elems := v.data.([]Value)
		parts := make([]string, len(elems))
		for i, e := range elems {
			parts[i] = e.String()
		}
		return fmt.Sprintf("[%s]", strings.Join(parts, ", "))
	case KindHash:
		entries := v.data.(map[string]Value)
		if len(entries) == 0 {
			return "{}"
		}
		parts := make([]string, 0, len(entries))
		for k, val := range entries {
			parts = append(parts, fmt.Sprintf("%s: %s", k, val.String()))
		}
		return fmt.Sprintf("{%s}", strings.Join(parts, ", "))
	case KindRange:
		r := v.data.(Range)
		return fmt.Sprintf("%d..%d", r.Start, r.End)
	case KindClass:
		cl := v.data.(*ClassDef)
		return fmt.Sprintf("<Class %s>", cl.Name)
	case KindInstance:
		inst := v.data.(*Instance)
		return fmt.Sprintf("<%s instance>", inst.Class.Name)
	default:
		return fmt.Sprintf("<%v>", v.kind)
	}
}

func (v Value) Truthy() bool {
	switch v.kind {
	case KindNil:
		return false
	case KindBool:
		return v.Bool()
	case KindInt:
		return v.data.(int64) != 0
	case KindFloat:
		return v.data.(float64) != 0
	case KindString:
		return v.data.(string) != ""
	case KindArray:
		return len(v.data.([]Value)) > 0
	case KindHash:
		return len(v.data.(map[string]Value)) > 0
	case KindClass, KindInstance:
		return true
	default:
		return true
	}
}

func (v Value) Equal(other Value) bool {
	if v.kind != other.kind {
		return false
	}
	switch v.kind {
	case KindNil:
		return true
	case KindBool:
		return v.Bool() == other.Bool()
	case KindInt:
		return v.data.(int64) == other.data.(int64)
	case KindFloat:
		return v.data.(float64) == other.data.(float64)
	case KindString, KindSymbol:
		return v.data.(string) == other.data.(string)
	case KindMoney:
		return v.data.(Money) == other.data.(Money)
	case KindDuration:
		return v.data.(Duration) == other.data.(Duration)
	case KindTime:
		return v.data.(time.Time).Equal(other.data.(time.Time))
	case KindRange:
		return v.data.(Range) == other.data.(Range)
	case KindClass:
		return v.data.(*ClassDef) == other.data.(*ClassDef)
	case KindInstance:
		return v.data.(*Instance) == other.data.(*Instance)
	default:
		return v.data == other.data
	}
}

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
func NewBlock(params []string, body []Statement, env *Env) Value {
	return Value{kind: KindBlock, data: &Block{Params: params, Body: body, Env: env}}
}

func NewClass(def *ClassDef) Value     { return Value{kind: KindClass, data: def} }
func NewInstance(inst *Instance) Value { return Value{kind: KindInstance, data: inst} }

type Builtin struct {
	Name       string
	Fn         BuiltinFunc
	AutoInvoke bool
}

type BuiltinFunc func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error)

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

func (v Value) Array() []Value {
	if v.kind != KindArray {
		return nil
	}
	return v.data.([]Value)
}

func (v Value) Hash() map[string]Value {
	if v.kind != KindHash && v.kind != KindObject {
		return nil
	}
	return v.data.(map[string]Value)
}

func (v Value) Class() *ClassDef {
	if v.kind != KindClass {
		return nil
	}
	return v.data.(*ClassDef)
}

func (v Value) Instance() *Instance {
	if v.kind != KindInstance {
		return nil
	}
	return v.data.(*Instance)
}

func (v Value) Money() Money {
	if v.kind != KindMoney {
		return Money{}
	}
	return v.data.(Money)
}

func (v Value) Duration() Duration {
	if v.kind != KindDuration {
		return Duration{}
	}
	return v.data.(Duration)
}

func (v Value) Time() time.Time {
	if v.kind != KindTime {
		return time.Time{}
	}
	return v.data.(time.Time)
}

func (v Value) Range() Range {
	if v.kind != KindRange {
		return Range{}
	}
	return v.data.(Range)
}

func (v Value) Function() *ScriptFunction {
	if v.kind != KindFunction {
		return nil
	}
	return v.data.(*ScriptFunction)
}

func (v Value) Builtin() *Builtin {
	if v.kind != KindBuiltin {
		return nil
	}
	return v.data.(*Builtin)
}

func (v Value) Block() *Block {
	if v.kind != KindBlock {
		return nil
	}
	return v.data.(*Block)
}

type Range struct {
	Start int64
	End   int64
}

type Block struct {
	Params []string
	Body   []Statement
	Env    *Env
}
