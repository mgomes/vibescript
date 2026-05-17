package vibes

import (
	"github.com/mgomes/vibescript/internal/runtime"
	"github.com/mgomes/vibescript/vibes/capability/contextcap"
)

// NewContextCapability constructs a data-only context CapabilityAdapter
// from the provided resolver. The adapter exposes the resolved
// attributes as a global on each script invocation.
func NewContextCapability(name string, resolver contextcap.Resolver) (CapabilityAdapter, error) {
	return runtime.NewContextCapability(name, resolver)
}

// MustNewContextCapability constructs a context CapabilityAdapter or
// panics when name is empty or resolver is a nil implementation.
func MustNewContextCapability(name string, resolver contextcap.Resolver) CapabilityAdapter {
	cap, err := NewContextCapability(name, resolver)
	if err != nil {
		panic(err)
	}
	return cap
}
