package vibes

import "context"

// CapabilityAdapter binds host capabilities into a script invocation.
type CapabilityAdapter interface {
	Bind(binding CapabilityBinding) (map[string]Value, error)
}

// CapabilityBinding provides execution context for adapters during binding.
type CapabilityBinding struct {
	Context context.Context
	Engine  *Engine
}
