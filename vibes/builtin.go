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
func NewBuiltin(name string, fn BuiltinFunc) value.Value {
	return runtime.MarkHostBuiltin(runtime.NewBuiltin(name, fn))
}

// NewAutoBuiltin returns a builtin function Value that auto-invokes without parentheses.
func NewAutoBuiltin(name string, fn BuiltinFunc) value.Value {
	return runtime.MarkHostBuiltin(runtime.NewAutoBuiltin(name, fn))
}

// DeclareNonMutating records a builtin's promise that no invocation of it
// writes to any container reachable from its receiver, arguments, keyword
// arguments, block, or from any execution's roots, and returns it. Allocating a
// container and filling it in is not such a write: the promise covers only
// state something else can already reach, so building a result and returning it
// keeps it. Script code the builtin drives through a block is not covered and
// does not need to be.
//
// This is a safety promise, not a performance hint. The runtime stops
// invalidating its memoized memory-estimator walk around calls to a builtin
// that makes it, so a declaration that is not true leaves an execution
// accounting for less memory than it actually holds, and that execution then
// allocates past the MemoryQuotaBytes it was configured with. A builtin that
// declares nothing keeps the existing conservative behavior, which is slower
// and correct; omission costs speed, never correctness.
//
// The promise is between an embedder and itself rather than between the sandbox
// and untrusted script. A host builtin already runs arbitrary Go in the
// embedding process and can allocate without bound and ignore every quota
// today, so declaring grants no capability a host lacked, and script code can
// neither observe the declaration nor reach it. It widens no sandbox boundary.
// What it does is switch off a backstop the host then has to honor itself.
//
// Apply it to a builtin whose whole body is known, and re-check it when that
// body changes.
func DeclareNonMutating(v value.Value) value.Value {
	return runtime.DeclareNonMutating(v)
}

// DeclareNonRetaining records a builtin's promise that no invocation of it
// stores, anywhere that outlives the invocation, a reference to any Value it
// receives (receiver, arguments, keyword arguments, block) or returns, or to
// any container reachable from one, and returns it. Package-level variables,
// fields on the adapter, closure captures, channels, caches and anything handed
// to another goroutine all count as outliving the invocation. So does keeping a
// container reached through an argument rather than the argument itself.
//
// Nothing in the runtime consults this promise yet: today it is recorded and
// no more, so declaring it changes no behavior and buys no speed. It is
// published now so the declaration exists before the change that reads it,
// which scopes the memory estimator's walk memo to the owning execution and
// will use it to keep that scoping across a host call (#1199).
//
// It is stated as a safety promise rather than a hint because of what it will
// mean once consulted: an execution calling a builtin that makes it keeps
// accounting for memory privately, so an untrue declaration would let a
// container the host kept be mutated afterwards without that execution
// observing the change, and its quota would then admit allocations it should
// have refused. Declare it only where that is true, not on the reasoning that
// it currently costs nothing.
//
// It is a separate promise from DeclareNonMutating and neither implies the
// other. A builtin that only reads its arguments but files one away for later
// is non-mutating and not non-retaining; one that overwrites a slot in its
// receiver and keeps nothing is the reverse.
func DeclareNonRetaining(v value.Value) value.Value {
	return runtime.DeclareNonRetaining(v)
}

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
	built, err := runtime.NewTypedBuiltin(name, fn, sig)
	if err != nil {
		return built, err
	}
	return runtime.MarkHostBuiltin(built), nil
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
