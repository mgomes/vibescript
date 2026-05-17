package vibes

import (
	"github.com/mgomes/vibescript/internal/runtime"
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
	return runtime.NewContextCapability(name, resolver)
}

// MustNewContextCapability is like NewContextCapability but panics if
// name or resolver is invalid. Intended for package-level variable
// initialization and tests where invalid input is a programmer error
// and recovery is not meaningful. In production code prefer
// NewContextCapability and handle the error.
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
