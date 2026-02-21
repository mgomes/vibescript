package vibes

import "fmt"

func (c *jobQueueCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	methods := map[string]Value{
		"enqueue": NewBuiltin(c.name+".enqueue", c.callEnqueue),
	}
	if c.retry != nil {
		methods["retry"] = NewBuiltin(c.name+".retry", c.callRetry)
	}
	return map[string]Value{c.name: NewObject(methods)}, nil
}

func (c *jobQueueCapability) callEnqueue(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 2 {
		return NewNil(), fmt.Errorf("%s.enqueue expects job name and payload", c.name)
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("%s.enqueue does not accept blocks", c.name)
	}

	jobNameVal := args[0]
	switch jobNameVal.Kind() {
	case KindString, KindSymbol:
		// supported
	default:
		return NewNil(), fmt.Errorf("%s.enqueue expects job name as string or symbol", c.name)
	}

	payloadVal := args[1]
	if payloadVal.Kind() != KindHash && payloadVal.Kind() != KindObject {
		return NewNil(), fmt.Errorf("%s.enqueue expects payload hash", c.name)
	}

	options, err := parseJobQueueEnqueueOptions(c.name, kwargs)
	if err != nil {
		return NewNil(), err
	}

	job := JobQueueJob{
		Name:    jobNameVal.String(),
		Payload: cloneHash(payloadVal.Hash()),
		Options: options,
	}

	result, err := c.queue.Enqueue(exec.ctx, job)
	if err != nil {
		return NewNil(), err
	}
	return cloneCapabilityMethodResult(c.name+".enqueue", result)
}

func (c *jobQueueCapability) callRetry(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if c.retry == nil {
		return NewNil(), fmt.Errorf("%s.retry is not supported", c.name)
	}
	if len(args) < 1 || len(args) > 2 {
		return NewNil(), fmt.Errorf("%s.retry expects job id and optional options hash", c.name)
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("%s.retry does not accept blocks", c.name)
	}

	idVal := args[0]
	if idVal.Kind() != KindString {
		return NewNil(), fmt.Errorf("%s.retry expects job id string", c.name)
	}

	options := make(map[string]Value)
	if len(args) > 1 {
		optsVal := args[1]
		if optsVal.Kind() != KindHash && optsVal.Kind() != KindObject {
			return NewNil(), fmt.Errorf("%s.retry options must be hash", c.name)
		}
		options = mergeHash(options, cloneHash(optsVal.Hash()))
	}
	options = mergeHash(options, cloneCapabilityKwargs(kwargs))

	req := JobQueueRetryRequest{JobID: idVal.String(), Options: options}
	result, err := c.retry.Retry(exec.ctx, req)
	if err != nil {
		return NewNil(), err
	}
	return cloneCapabilityMethodResult(c.name+".retry", result)
}
