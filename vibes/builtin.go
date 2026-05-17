package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// Builtin represents a built-in function callable from Vibescript.
type Builtin = runtime.Builtin

// BuiltinFunc is the Go function signature for built-in Vibescript functions.
type BuiltinFunc = runtime.BuiltinFunc

// Block represents a closure passed to a function at runtime.
type Block = runtime.Block

// NewBuiltin returns a builtin function Value.
func NewBuiltin(name string, fn BuiltinFunc) Value { return runtime.NewBuiltin(name, fn) }

// NewAutoBuiltin returns a builtin function Value that auto-invokes without parentheses.
func NewAutoBuiltin(name string, fn BuiltinFunc) Value { return runtime.NewAutoBuiltin(name, fn) }

// NewBlock returns a block (closure) Value.
func NewBlock(params []Param, body []Statement, env *Env) Value {
	return runtime.NewBlock(params, body, env)
}

// BuiltinOf returns the *Builtin stored in v, or nil.
func BuiltinOf(v Value) *Builtin { return runtime.BuiltinOf(v) }

// BlockOf returns the *Block stored in v, or nil.
func BlockOf(v Value) *Block { return runtime.BlockOf(v) }

// Builtins maps builtin function names to their Value implementations.
type Builtins = map[string]Value
