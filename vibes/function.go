package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// NewFunction returns a script-defined function Value.
func NewFunction(fn *ScriptFunction) Value { return runtime.NewFunction(fn) }

// FunctionOf returns the *ScriptFunction stored in v, or nil.
func FunctionOf(v Value) *ScriptFunction { return runtime.FunctionOf(v) }
