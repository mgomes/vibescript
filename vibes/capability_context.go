package vibes

import (
	"context"
	"fmt"
)

// ContextCapabilityResolver resolves call-scoped context data for script access.
type ContextCapabilityResolver func(ctx context.Context) (Value, error)

// NewContextCapability constructs a data-only context capability adapter.
func NewContextCapability(name string, resolver ContextCapabilityResolver) (CapabilityAdapter, error) {
	if name == "" {
		return nil, fmt.Errorf("vibes: context capability name must be non-empty")
	}
	if resolver == nil {
		return nil, fmt.Errorf("vibes: context capability requires a resolver")
	}
	return &contextCapability{name: name, resolver: resolver}, nil
}

// MustNewContextCapability constructs a capability adapter or panics on invalid arguments.
func MustNewContextCapability(name string, resolver ContextCapabilityResolver) CapabilityAdapter {
	cap, err := NewContextCapability(name, resolver)
	if err != nil {
		panic(err)
	}
	return cap
}

type contextCapability struct {
	name     string
	resolver ContextCapabilityResolver
}

func (c *contextCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	val, err := c.resolver(binding.Context)
	if err != nil {
		return nil, err
	}
	if val.Kind() != KindHash && val.Kind() != KindObject {
		return nil, fmt.Errorf("%s capability resolver must return hash/object", c.name)
	}
	if err := validateCapabilityDataOnlyValue(c.name+" capability value", val); err != nil {
		return nil, err
	}
	return map[string]Value{c.name: deepCloneValue(val)}, nil
}
