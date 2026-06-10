package vibes

import (
	"github.com/mgomes/vibescript/internal/runtime"
	"github.com/mgomes/vibescript/vibes/value"
)

// Builtin represents a built-in function callable from Vibescript.
type Builtin = runtime.Builtin

// BuiltinFunc is the Go function signature for built-in Vibescript functions.
type BuiltinFunc = runtime.BuiltinFunc

// NewBuiltin returns a builtin function Value.
func NewBuiltin(name string, fn BuiltinFunc) value.Value { return runtime.NewBuiltin(name, fn) }

// NewAutoBuiltin returns a builtin function Value that auto-invokes without parentheses.
func NewAutoBuiltin(name string, fn BuiltinFunc) value.Value { return runtime.NewAutoBuiltin(name, fn) }

// Builtins maps builtin function names to their Value implementations.
type Builtins = map[string]value.Value

// MemberCompletionNames returns the builtin member-method names per
// receiver type (string, array, hash, int, float, money, duration,
// time), for editor tooling such as LSP completion.
func MemberCompletionNames() map[string][]string {
	return runtime.MemberCompletionNames()
}
