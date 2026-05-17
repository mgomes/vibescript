package runtime

import (
	"github.com/mgomes/vibescript/vibes/capability/events"
)

// Internal aliases for events capability types so runtime code (and tests)
// can keep referring to short names that match the public vibes facade.
type (
	EventPublisher      = events.Publisher
	EventPublishRequest = events.PublishRequest
)

// NewEventsCapability constructs a CapabilityAdapter that delegates to a
// *events.Capability. The vibes facade re-exports this entry point under
// the same name.
func NewEventsCapability(name string, publisher EventPublisher) (CapabilityAdapter, error) {
	inner, err := events.NewCapability(name, publisher)
	if err != nil {
		return nil, err
	}
	return &eventsCapability{inner: inner}, nil
}

// MustNewEventsCapability is the panicking variant of NewEventsCapability.
func MustNewEventsCapability(name string, publisher EventPublisher) CapabilityAdapter {
	cap, err := NewEventsCapability(name, publisher)
	if err != nil {
		panic(err)
	}
	return cap
}

// eventsCapability bridges an *events.Capability into the runtime by
// implementing CapabilityAdapter and CapabilityContractProvider.
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
	result, err := c.inner.Publish(exec.Context(), args, kwargs, !block.IsNil())
	if err != nil {
		return NewNil(), err
	}
	return result, nil
}

func (c *eventsCapability) validatePublishArgs(args []Value, kwargs map[string]Value, block Value) error {
	return c.inner.ValidatePublishArgs(args, kwargs, !block.IsNil())
}
