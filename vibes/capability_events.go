package vibes

import (
	"github.com/mgomes/vibescript/internal/runtime"
	"github.com/mgomes/vibescript/vibes/capability/events"
)

// NewEventsCapability constructs an events CapabilityAdapter bound to
// the provided script-facing name. The adapter wraps an
// *events.Capability built from publisher and dispatches the publish
// builtin.
func NewEventsCapability(name string, publisher events.Publisher) (CapabilityAdapter, error) {
	return runtime.NewEventsCapability(name, publisher)
}

// MustNewEventsCapability constructs an events CapabilityAdapter or
// panics when name is empty or publisher is a nil implementation.
func MustNewEventsCapability(name string, publisher events.Publisher) CapabilityAdapter {
	cap, err := NewEventsCapability(name, publisher)
	if err != nil {
		panic(err)
	}
	return cap
}
