package vibes

import "context"

// CapabilityAdapter binds host capabilities into a script invocation.
type CapabilityAdapter interface {
	Bind(binding CapabilityBinding) (map[string]Value, error)
}

// CapabilityMethodContract validates capability method calls at the boundary.
// These contracts run before and after a capability builtin executes.
type CapabilityMethodContract struct {
	ValidateArgs   func(args []Value, kwargs map[string]Value, block Value) error
	ValidateReturn func(result Value) error
}

// CapabilityContractProvider exposes per-method contracts for capability adapters.
// Contract keys must match builtin method names exposed to scripts (for example "jobs.enqueue").
type CapabilityContractProvider interface {
	CapabilityContracts() map[string]CapabilityMethodContract
}

// CapabilityBinding provides execution context for adapters during binding.
type CapabilityBinding struct {
	Context context.Context
	Engine  *Engine
}
