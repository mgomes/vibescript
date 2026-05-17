package runtime

import (
	"fmt"

	"github.com/mgomes/vibescript/vibes/capability/jobqueue"
)

// Internal aliases for jobqueue capability types so runtime code (and
// tests) can keep referring to short names that match the public vibes
// facade.
type (
	JobQueue               = jobqueue.JobQueue
	JobQueueWithRetry      = jobqueue.JobQueueWithRetry
	JobQueueJob            = jobqueue.JobQueueJob
	JobQueueEnqueueOptions = jobqueue.JobQueueEnqueueOptions
	JobQueueRetryRequest   = jobqueue.JobQueueRetryRequest
)

// jobQueueCapability adapts a *jobqueue.Capability into the
// CapabilityAdapter and CapabilityContractProvider interfaces consumed
// by the runtime. Bind exposes enqueue (and retry when supported) as
// builtin methods under the capability name.
type jobQueueCapability struct {
	inner *jobqueue.Capability
}

// NewJobQueueCapability constructs a CapabilityAdapter that delegates to a
// *jobqueue.Capability. It is the runtime-facing entry point used by the
// vibes facade.
func NewJobQueueCapability(name string, impl JobQueue) (CapabilityAdapter, error) {
	inner, err := jobqueue.NewCapability(name, impl)
	if err != nil {
		return nil, err
	}
	return &jobQueueCapability{inner: inner}, nil
}

// MustNewJobQueueCapability is the panicking variant of NewJobQueueCapability.
func MustNewJobQueueCapability(name string, impl JobQueue) CapabilityAdapter {
	cap, err := NewJobQueueCapability(name, impl)
	if err != nil {
		panic(err)
	}
	return cap
}

func (c *jobQueueCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	name := c.inner.Name
	methods := map[string]Value{
		"enqueue": NewBuiltin(name+".enqueue", c.callEnqueue),
	}
	if c.inner.HasRetry() {
		methods["retry"] = NewBuiltin(name+".retry", c.callRetry)
	}
	return map[string]Value{name: NewObject(methods)}, nil
}

func (c *jobQueueCapability) callEnqueue(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	name := c.inner.Name
	if len(args) != 2 {
		return NewNil(), fmt.Errorf("%s.enqueue expects job name and payload", name)
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("%s.enqueue does not accept blocks", name)
	}

	jobNameVal := args[0]
	switch jobNameVal.Kind() {
	case KindString, KindSymbol:
		// supported
	default:
		return NewNil(), fmt.Errorf("%s.enqueue expects job name as string or symbol", name)
	}

	payloadVal := args[1]
	if payloadVal.Kind() != KindHash && payloadVal.Kind() != KindObject {
		return NewNil(), fmt.Errorf("%s.enqueue expects payload hash", name)
	}

	options, err := jobqueue.ParseEnqueueOptions(name, kwargs)
	if err != nil {
		return NewNil(), err
	}

	job := jobqueue.JobQueueJob{
		Name:    jobNameVal.String(),
		Payload: cloneHash(payloadVal.Hash()),
		Options: options,
	}

	result, err := c.inner.Queue.Enqueue(exec.Context(), job)
	if err != nil {
		return NewNil(), err
	}
	return cloneCapabilityMethodResult(name+".enqueue", result)
}

func (c *jobQueueCapability) callRetry(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	name := c.inner.Name
	if c.inner.Retry == nil {
		return NewNil(), fmt.Errorf("%s.retry is not supported", name)
	}
	if len(args) < 1 || len(args) > 2 {
		return NewNil(), fmt.Errorf("%s.retry expects job id and optional options hash", name)
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("%s.retry does not accept blocks", name)
	}

	idVal := args[0]
	if idVal.Kind() != KindString {
		return NewNil(), fmt.Errorf("%s.retry expects job id string", name)
	}

	options := make(map[string]Value)
	if len(args) > 1 {
		optsVal := args[1]
		if optsVal.Kind() != KindHash && optsVal.Kind() != KindObject {
			return NewNil(), fmt.Errorf("%s.retry options must be hash", name)
		}
		options = mergeHash(options, cloneHash(optsVal.Hash()))
	}
	options = mergeHash(options, cloneCapabilityKwargs(kwargs))

	req := jobqueue.JobQueueRetryRequest{JobID: idVal.String(), Options: options}
	result, err := c.inner.Retry.Retry(exec.Context(), req)
	if err != nil {
		return NewNil(), err
	}
	return cloneCapabilityMethodResult(name+".retry", result)
}

func (c *jobQueueCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	name := c.inner.Name
	contracts := map[string]CapabilityMethodContract{
		name + ".enqueue": {
			ValidateArgs:   c.validateEnqueueContractArgs,
			ValidateReturn: capabilityValidateAnyReturn(name + ".enqueue"),
		},
	}
	if c.inner.HasRetry() {
		contracts[name+".retry"] = CapabilityMethodContract{
			ValidateArgs:   c.validateRetryContractArgs,
			ValidateReturn: capabilityValidateAnyReturn(name + ".retry"),
		}
	}
	return contracts
}

func (c *jobQueueCapability) validateEnqueueContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.inner.Name + ".enqueue"

	if len(args) != 2 {
		return fmt.Errorf("%s expects job name and payload", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}

	jobNameVal := args[0]
	switch jobNameVal.Kind() {
	case KindString, KindSymbol:
		// supported
	default:
		return fmt.Errorf("%s expects job name as string or symbol", method)
	}

	if err := validateCapabilityHashValue(method+" payload", args[1]); err != nil {
		return err
	}

	return validateCapabilityKwargsDataOnly(method, kwargs)
}

func (c *jobQueueCapability) validateRetryContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.inner.Name + ".retry"

	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("%s expects job id and optional options hash", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}

	idVal := args[0]
	if idVal.Kind() != KindString {
		return fmt.Errorf("%s expects job id string", method)
	}

	if len(args) == 2 {
		if err := validateCapabilityHashValue(method+" options", args[1]); err != nil {
			return err
		}
	}

	return validateCapabilityKwargsDataOnly(method, kwargs)
}
