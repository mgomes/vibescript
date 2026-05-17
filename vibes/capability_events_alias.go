package vibes

import (
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
	inner, err := events.NewCapability(name, publisher)
	if err != nil {
		return nil, err
	}
	return &eventsCapability{inner: inner}, nil
}

// MustNewEventsCapability is the panicking variant of NewEventsCapability.
//
// Deprecated: use events.MustNewCapability. Will be removed in v0.29.0.
func MustNewEventsCapability(name string, publisher EventPublisher) CapabilityAdapter {
	cap, err := NewEventsCapability(name, publisher)
	if err != nil {
		panic(err)
	}
	return cap
}

// eventsCapability bridges an *events.Capability into the vibes runtime by
// implementing CapabilityAdapter and CapabilityContractProvider. It lives in
// vibes because it needs *Execution.ctx access and Builtin construction,
// neither of which the carved subpackage can reach without an import cycle.
type eventsCapability struct {
	inner *events.Capability
}

func (c *eventsCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	method := c.inner.PublishMethodName()
	return map[string]CapabilityMethodContract{
		method: {
			ValidateArgs:   c.validatePublishArgs,
			ValidateReturn: c.inner.ValidatePublishReturn,
		},
	}
}

func (c *eventsCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	methods := map[string]Value{
		"publish": NewBuiltin(c.inner.PublishMethodName(), c.callPublish),
	}
	return map[string]Value{c.inner.Name: NewObject(methods)}, nil
}

func (c *eventsCapability) callPublish(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	result, err := c.inner.Publish(exec.ctx, args, kwargs, !block.IsNil())
	if err != nil {
		return NewNil(), err
	}
	return result, nil
}

func (c *eventsCapability) validatePublishArgs(args []Value, kwargs map[string]Value, block Value) error {
	return c.inner.ValidatePublishArgs(args, kwargs, !block.IsNil())
}
