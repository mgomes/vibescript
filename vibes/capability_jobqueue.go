package vibes

import (
	"context"
	"fmt"
	"maps"
	"time"
)

// JobQueue exposes queue functionality to scripts via strongly-typed adapters.
type JobQueue interface {
	Enqueue(ctx context.Context, job JobQueueJob) (Value, error)
}

// JobQueueWithRetry extends JobQueue with a retry operation.
type JobQueueWithRetry interface {
	JobQueue
	Retry(ctx context.Context, req JobQueueRetryRequest) (Value, error)
}

// JobQueueJob captures a job invocation from script code.
type JobQueueJob struct {
	Name    string
	Payload map[string]Value
	Options JobQueueEnqueueOptions
}

// JobQueueEnqueueOptions represent keyword arguments supplied to enqueue.
type JobQueueEnqueueOptions struct {
	Delay  *time.Duration
	Key    *string
	Kwargs map[string]Value
}

// JobQueueRetryRequest captures retry invocations.
type JobQueueRetryRequest struct {
	JobID   string
	Options map[string]Value
}

// NewJobQueueCapability constructs a capability adapter bound to the provided name.
func NewJobQueueCapability(name string, queue JobQueue) (CapabilityAdapter, error) {
	if name == "" {
		return nil, fmt.Errorf("vibes: job queue capability name must be non-empty")
	}
	if queue == nil {
		return nil, fmt.Errorf("vibes: job queue capability requires a non-nil implementation")
	}

	cap := &jobQueueCapability{name: name, queue: queue}
	if retry, ok := queue.(JobQueueWithRetry); ok {
		cap.retry = retry
	}
	return cap, nil
}

// MustNewJobQueueCapability constructs a capability adapter or panics on invalid arguments.
func MustNewJobQueueCapability(name string, queue JobQueue) CapabilityAdapter {
	cap, err := NewJobQueueCapability(name, queue)
	if err != nil {
		panic(err)
	}
	return cap
}

type jobQueueCapability struct {
	name  string
	queue JobQueue
	retry JobQueueWithRetry
}

func (c *jobQueueCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	contracts := map[string]CapabilityMethodContract{
		c.name + ".enqueue": {
			ValidateArgs:   c.validateEnqueueContractArgs,
			ValidateReturn: c.validateMethodReturn(c.name + ".enqueue"),
		},
	}
	if c.retry != nil {
		contracts[c.name+".retry"] = CapabilityMethodContract{
			ValidateArgs:   c.validateRetryContractArgs,
			ValidateReturn: c.validateMethodReturn(c.name + ".retry"),
		}
	}
	return contracts
}

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

	return c.queue.Enqueue(exec.ctx, job)
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
		options = mergeHash(options, optsVal.Hash())
	}
	options = mergeHash(options, kwargs)

	req := JobQueueRetryRequest{JobID: idVal.String(), Options: options}
	return c.retry.Retry(exec.ctx, req)
}

func (c *jobQueueCapability) validateEnqueueContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.name + ".enqueue"

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

	payloadVal := args[1]
	if payloadVal.Kind() != KindHash && payloadVal.Kind() != KindObject {
		return fmt.Errorf("%s expects payload hash", method)
	}
	if err := validateCapabilityDataOnlyValue(method+" payload", payloadVal); err != nil {
		return err
	}

	for key, val := range kwargs {
		if err := validateCapabilityDataOnlyValue(fmt.Sprintf("%s keyword %s", method, key), val); err != nil {
			return err
		}
	}

	return nil
}

func (c *jobQueueCapability) validateRetryContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.name + ".retry"

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
		optionsVal := args[1]
		if optionsVal.Kind() != KindHash && optionsVal.Kind() != KindObject {
			return fmt.Errorf("%s options must be hash", method)
		}
		if err := validateCapabilityDataOnlyValue(method+" options", optionsVal); err != nil {
			return err
		}
	}

	for key, val := range kwargs {
		if err := validateCapabilityDataOnlyValue(fmt.Sprintf("%s keyword %s", method, key), val); err != nil {
			return err
		}
	}

	return nil
}

func (c *jobQueueCapability) validateMethodReturn(method string) func(result Value) error {
	return func(result Value) error {
		return validateCapabilityDataOnlyValue(method+" return value", result)
	}
}

func parseJobQueueEnqueueOptions(name string, kwargs map[string]Value) (JobQueueEnqueueOptions, error) {
	if len(kwargs) == 0 {
		return JobQueueEnqueueOptions{}, nil
	}

	var delay *time.Duration
	var key *string
	extra := make(map[string]Value)

	for k, v := range kwargs {
		switch k {
		case "delay":
			d, err := valueToTimeDuration(name, v)
			if err != nil {
				return JobQueueEnqueueOptions{}, err
			}
			if d < 0 {
				return JobQueueEnqueueOptions{}, fmt.Errorf("%s.enqueue delay must be non-negative", name)
			}
			delay = &d
		case "key":
			if v.Kind() != KindString {
				return JobQueueEnqueueOptions{}, fmt.Errorf("%s.enqueue key must be a string", name)
			}
			s := v.String()
			if s == "" {
				return JobQueueEnqueueOptions{}, fmt.Errorf("%s.enqueue key must be non-empty", name)
			}
			key = &s
		default:
			extra[k] = v
		}
	}

	opts := JobQueueEnqueueOptions{}
	opts.Delay = delay
	opts.Key = key
	if len(extra) > 0 {
		opts.Kwargs = extra
	}
	return opts, nil
}

func valueToTimeDuration(name string, val Value) (time.Duration, error) {
	switch val.Kind() {
	case KindDuration:
		secs := val.Duration().Seconds()
		return time.Duration(secs) * time.Second, nil
	case KindInt, KindFloat:
		secs, err := valueToInt64(val)
		if err != nil {
			return 0, err
		}
		return time.Duration(secs) * time.Second, nil
	default:
		return 0, fmt.Errorf("%s.enqueue delay must be duration or numeric seconds", name)
	}
}

// cloneHash creates a deep copy of a hash to prevent mutations from affecting the original.
// For Values that contain references (arrays, hashes, objects), this recursively clones them.
func cloneHash(src map[string]Value) map[string]Value {
	if len(src) == 0 {
		return map[string]Value{}
	}
	out := make(map[string]Value, len(src))
	for k, v := range src {
		out[k] = deepCloneValue(v)
	}
	return out
}

// deepCloneValue recursively clones a Value and its contents.
func deepCloneValue(val Value) Value {
	switch val.Kind() {
	case KindArray:
		arr := val.Array()
		cloned := make([]Value, len(arr))
		for i, elem := range arr {
			cloned[i] = deepCloneValue(elem)
		}
		return NewArray(cloned)
	case KindHash:
		hash := val.Hash()
		cloned := make(map[string]Value, len(hash))
		for k, v := range hash {
			cloned[k] = deepCloneValue(v)
		}
		return NewHash(cloned)
	case KindObject:
		obj := val.Hash()
		cloned := make(map[string]Value, len(obj))
		for k, v := range obj {
			cloned[k] = deepCloneValue(v)
		}
		return NewObject(cloned)
	default:
		// Primitive types (string, int, float, bool, nil, symbol, duration, money) are immutable or value types
		return val
	}
}

func mergeHash(dest map[string]Value, src map[string]Value) map[string]Value {
	if len(src) == 0 {
		return dest
	}
	if dest == nil {
		dest = make(map[string]Value, len(src))
	}
	maps.Copy(dest, src)
	return dest
}
