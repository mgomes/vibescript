package vibes

import (
	"github.com/mgomes/vibescript/vibes/capability/contextcap"
)

// ContextCapabilityResolver is an alias for contextcap.Resolver kept so
// existing embedders continue to compile. It will be removed in v0.29.0.
type ContextCapabilityResolver = contextcap.Resolver

// NewContextCapability constructs a data-only context capability adapter.
//
// Deprecated: prefer contextcap.NewCapability and wrap the result via the
// vibes facade when a CapabilityAdapter is required. Will be removed in
// v0.29.0.
func NewContextCapability(name string, resolver ContextCapabilityResolver) (CapabilityAdapter, error) {
	inner, err := contextcap.NewCapability(name, resolver)
	if err != nil {
		return nil, err
	}
	return &contextCapabilityAdapter{inner: inner}, nil
}

// MustNewContextCapability constructs a context capability or panics.
//
// Deprecated: prefer contextcap.MustNewCapability. Will be removed in
// v0.29.0.
func MustNewContextCapability(name string, resolver ContextCapabilityResolver) CapabilityAdapter {
	cap, err := NewContextCapability(name, resolver)
	if err != nil {
		panic(err)
	}
	return cap
}

// contextCapabilityAdapter bridges contextcap.Capability into vibes's
// CapabilityAdapter interface, which is anchored on the vibes-side
// CapabilityBinding type and cannot live in the carved subpackage.
type contextCapabilityAdapter struct {
	inner *contextcap.Capability
}

func (a *contextCapabilityAdapter) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return a.inner.Bind(binding.Context)
}
