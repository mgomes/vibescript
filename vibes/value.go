package vibes

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

type Value struct {
	kind ValueKind
	data any
}

type Builtin struct {
	Name       string
	Fn         BuiltinFunc
	AutoInvoke bool
}

type BuiltinFunc func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error)

type Range struct {
	Start int64
	End   int64
}

type Block struct {
	Params     []Param
	Body       []Statement
	Env        *Env
	moduleKey  string
	modulePath string
	moduleRoot string
}
