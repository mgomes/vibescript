// Package jobqueue defines the host-facing contract for the job-queue
// capability that Vibescript exposes to scripts. The runtime wraps a
// *Capability with a script-visible adapter; embedders implement
// JobQueue (and optionally JobQueueWithRetry) to back the methods.
package jobqueue

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/mgomes/vibescript/vibes/value"
)

// JobQueue exposes queue functionality to scripts via strongly-typed adapters.
type JobQueue interface {
	Enqueue(ctx context.Context, job JobQueueJob) (value.Value, error)
}

// JobQueueWithRetry extends JobQueue with a retry operation.
type JobQueueWithRetry interface {
	JobQueue
	Retry(ctx context.Context, req JobQueueRetryRequest) (value.Value, error)
}

// JobQueueJob captures a job invocation from script code.
type JobQueueJob struct {
	Name    string
	Payload map[string]value.Value
	Options JobQueueEnqueueOptions
}

// JobQueueEnqueueOptions represents keyword arguments supplied to enqueue.
type JobQueueEnqueueOptions struct {
	Delay  *time.Duration
	Key    *string
	Kwargs map[string]value.Value
}

// JobQueueRetryRequest captures retry invocations.
type JobQueueRetryRequest struct {
	JobID   string
	Options map[string]value.Value
}

// Capability binds a host JobQueue implementation under a script-visible
// name. The vibes package wraps it in a CapabilityAdapter; embedders
// construct one via NewCapability.
type Capability struct {
	Name  string
	Queue JobQueue
	Retry JobQueueWithRetry
}

// NewCapability validates the inputs and returns a bound Capability. It
// returns an error when name is empty or when queue is a nil
// implementation (typed or untyped).
func NewCapability(name string, queue JobQueue) (*Capability, error) {
	if name == "" {
		return nil, fmt.Errorf("vibes: job queue capability name must be non-empty")
	}
	if isNilImpl(queue) {
		return nil, fmt.Errorf("vibes: job queue capability requires a non-nil implementation")
	}
	cap := &Capability{Name: name, Queue: queue}
	if retry, ok := queue.(JobQueueWithRetry); ok {
		cap.Retry = retry
	}
	return cap, nil
}

// MustNewCapability is the panicking variant of NewCapability.
func MustNewCapability(name string, queue JobQueue) *Capability {
	cap, err := NewCapability(name, queue)
	if err != nil {
		panic(err)
	}
	return cap
}

// HasRetry reports whether the bound implementation supports retry.
func (c *Capability) HasRetry() bool { return c.Retry != nil }

// ParseEnqueueOptions converts kwargs received from a script into a
// structured JobQueueEnqueueOptions value. The name is used for error
// messages so they line up with the script-visible capability name.
func ParseEnqueueOptions(name string, kwargs map[string]value.Value) (JobQueueEnqueueOptions, error) {
	if len(kwargs) == 0 {
		return JobQueueEnqueueOptions{}, nil
	}

	var delay *time.Duration
	var key *string
	extra := make(map[string]value.Value)

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
			if v.Kind() != value.KindString {
				return JobQueueEnqueueOptions{}, fmt.Errorf("%s.enqueue key must be a string", name)
			}
			s := v.String()
			if s == "" {
				return JobQueueEnqueueOptions{}, fmt.Errorf("%s.enqueue key must be non-empty", name)
			}
			key = &s
		default:
			extra[k] = deepCloneValue(v)
		}
	}

	opts := JobQueueEnqueueOptions{Delay: delay, Key: key}
	if len(extra) > 0 {
		opts.Kwargs = extra
	}
	return opts, nil
}

func valueToTimeDuration(name string, val value.Value) (time.Duration, error) {
	switch val.Kind() {
	case value.KindDuration:
		secs := val.Duration().Seconds()
		return time.Duration(secs) * time.Second, nil
	case value.KindInt, value.KindFloat:
		secs, err := value.ValueToInt64(val)
		if err != nil {
			return 0, err
		}
		return time.Duration(secs) * time.Second, nil
	default:
		return 0, fmt.Errorf("%s.enqueue delay must be duration or numeric seconds", name)
	}
}

// isNilImpl reports whether impl is an untyped or typed nil. It is
// duplicated here rather than imported from vibes to keep this package
// free of an import cycle.
func isNilImpl(impl any) bool {
	if impl == nil {
		return true
	}
	val := reflect.ValueOf(impl)
	switch val.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return val.IsNil()
	default:
		return false
	}
}

// deepCloneValue mirrors vibes' deepCloneValue for data-only kinds so
// option parsing can defensively clone hash arguments without reaching
// back into vibes. Runtime-only kinds (block, builtin, class, ...) are
// rejected upstream in vibes' capability contracts.
func deepCloneValue(v value.Value) value.Value {
	switch v.Kind() {
	case value.KindArray:
		arr := v.Array()
		cloned := make([]value.Value, len(arr))
		for i, elem := range arr {
			cloned[i] = deepCloneValue(elem)
		}
		return value.NewArray(cloned)
	case value.KindHash:
		hash := v.Hash()
		cloned := make(map[string]value.Value, len(hash))
		for k, val := range hash {
			cloned[k] = deepCloneValue(val)
		}
		return value.NewHash(cloned)
	case value.KindObject:
		obj := v.Hash()
		cloned := make(map[string]value.Value, len(obj))
		for k, val := range obj {
			cloned[k] = deepCloneValue(val)
		}
		return value.NewObject(cloned)
	default:
		return v
	}
}
