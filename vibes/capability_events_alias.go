package vibes

import (
	"github.com/mgomes/vibescript/internal/runtime"
	"github.com/mgomes/vibescript/vibes/capability/events"
)

// Type aliases for the events capability types that moved to
// vibes/capability/events. These keep embedders that import vibes
// source-compatible during the transition. Removed in v0.29.0.
type (
	EventPublisher      = events.Publisher
	EventPublishRequest = events.PublishRequest
)

// NewEventsCapability constructs a CapabilityAdapter bound to the provided
// name. It returns an adapter that delegates to a *events.Capability.
//
// Deprecated: use events.NewCapability and wrap the result via the runtime
// when binding. Will be removed in v0.29.0.
func NewEventsCapability(name string, publisher EventPublisher) (CapabilityAdapter, error) {
	return runtime.NewEventsCapability(name, publisher)
}

// MustNewEventsCapability is like NewEventsCapability but panics if
// name or publisher is invalid. Intended for package-level variable
// initialization and tests where invalid input is a programmer error
// and recovery is not meaningful. In production code prefer
// NewEventsCapability and handle the error.
//
// Deprecated: use events.MustNewCapability. Will be removed in v0.29.0.
func MustNewEventsCapability(name string, publisher EventPublisher) CapabilityAdapter {
	cap, err := NewEventsCapability(name, publisher)
	if err != nil {
		panic(err)
	}
	return cap
}
