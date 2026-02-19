package vibes

import (
	"context"
	"fmt"
)

// EventPublisher exposes event publication capability methods to scripts.
type EventPublisher interface {
	Publish(ctx context.Context, req EventPublishRequest) (Value, error)
}

// EventPublishRequest captures events.publish calls.
type EventPublishRequest struct {
	Topic   string
	Payload map[string]Value
	Options map[string]Value
}

// NewEventsCapability constructs a capability adapter bound to the provided name.
func NewEventsCapability(name string, publisher EventPublisher) (CapabilityAdapter, error) {
	if name == "" {
		return nil, fmt.Errorf("vibes: events capability name must be non-empty")
	}
	if isNilCapabilityImplementation(publisher) {
		return nil, fmt.Errorf("vibes: events capability requires a non-nil implementation")
	}
	return &eventsCapability{name: name, publisher: publisher}, nil
}

// MustNewEventsCapability constructs a capability adapter or panics on invalid arguments.
func MustNewEventsCapability(name string, publisher EventPublisher) CapabilityAdapter {
	cap, err := NewEventsCapability(name, publisher)
	if err != nil {
		panic(err)
	}
	return cap
}

type eventsCapability struct {
	name      string
	publisher EventPublisher
}

func (c *eventsCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	method := c.name + ".publish"
	return map[string]CapabilityMethodContract{
		method: {
			ValidateArgs:   c.validatePublishContractArgs,
			ValidateReturn: c.validateMethodReturn(method),
		},
	}
}

func (c *eventsCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	methods := map[string]Value{
		"publish": NewBuiltin(c.name+".publish", c.callPublish),
	}
	return map[string]Value{c.name: NewObject(methods)}, nil
}

func (c *eventsCapability) callPublish(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if err := c.validatePublishContractArgs(args, kwargs, block); err != nil {
		return NewNil(), err
	}
	topic, _ := capabilityNameArg(c.name+".publish", "topic", args[0])
	req := EventPublishRequest{
		Topic:   topic,
		Payload: cloneHash(args[1].Hash()),
		Options: cloneCapabilityKwargs(kwargs),
	}
	result, err := c.publisher.Publish(exec.ctx, req)
	if err != nil {
		return NewNil(), err
	}
	return c.cloneMethodResult(c.name+".publish", result)
}

func (c *eventsCapability) validatePublishContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.name + ".publish"
	if len(args) != 2 {
		return fmt.Errorf("%s expects topic and payload", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}
	if _, err := capabilityNameArg(method, "topic", args[0]); err != nil {
		return err
	}
	if err := validateCapabilityHashValue(method+" payload", args[1]); err != nil {
		return err
	}
	return validateCapabilityKwargsDataOnly(method, kwargs)
}

func (c *eventsCapability) validateMethodReturn(method string) func(result Value) error {
	return func(result Value) error {
		return validateCapabilityTypedValue(method+" return value", result, capabilityTypeAny)
	}
}

func (c *eventsCapability) cloneMethodResult(method string, result Value) (Value, error) {
	if err := validateCapabilityTypedValue(method+" return value", result, capabilityTypeAny); err != nil {
		return NewNil(), err
	}
	return deepCloneValue(result), nil
}
