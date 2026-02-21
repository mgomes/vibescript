package vibes

import (
	"context"
	"fmt"
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
	if isNilCapabilityImplementation(queue) {
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
