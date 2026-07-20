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

// Signature is the opt-in static contract a host callable publishes to the
// checker; the same contract is enforced at runtime. See NewTypedBuiltin and
// Engine.RegisterBuiltinWithSignature.
type Signature = runtime.Signature

// SignatureParam declares one positional parameter of a host callable's
// published Signature.
type SignatureParam = runtime.SignatureParam

// NewTypedBuiltin returns a builtin function Value that publishes sig to the
// checker and validates calls against it at runtime. The value can be
// registered as an engine builtin, passed as a call-option global, or exposed
// as a capability method.
func NewTypedBuiltin(name string, fn BuiltinFunc, sig Signature) (value.Value, error) {
	return runtime.NewTypedBuiltin(name, fn, sig)
}

// Builtins maps builtin function names to their Value implementations.
type Builtins = map[string]value.Value

// MemberCompletionNames returns the builtin member-method names per
// receiver type (string, array, hash, int, float, money, duration,
// time), for editor tooling such as LSP completion.
func MemberCompletionNames() map[string][]string {
	return runtime.MemberCompletionNames()
}

// MemberParam is one positional parameter of a builtin member contract.
type MemberParam = runtime.MemberParam

// MemberContract is the registered contract of one builtin member method:
// receiver kind, name and aliases, call shape, and effect metadata.
type MemberContract = runtime.MemberContract

// MemberContracts returns the registered builtin member contracts, for
// editor tooling such as LSP completion. The runtime registry backing it
// also drives the static checker's member call validation.
func MemberContracts() []MemberContract {
	return runtime.MemberContracts()
}
