package vibes

// ValueKind identifies the type of a runtime Value.
type ValueKind int

const (
	// KindNil is the nil value kind.
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
	KindEnum
	KindEnumValue
	KindClass
	KindInstance
)

// Value is a tagged union holding any Vibescript runtime value.
type Value struct {
	kind ValueKind
	data any
}

// Builtin represents a built-in function callable from Vibescript.
type Builtin struct {
	Name       string
	Fn         BuiltinFunc
	AutoInvoke bool
}

// BuiltinFunc is the Go function signature for built-in Vibescript functions.
type BuiltinFunc func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error)

// Range represents an integer range with inclusive start and end.
type Range struct {
	Start int64
	End   int64
}

// Block represents a closure passed to a function at runtime.
type Block struct {
	Params     []Param
	Body       []Statement
	Env        *Env
	owner      *Script
	moduleKey  string
	modulePath string
	moduleRoot string
}
