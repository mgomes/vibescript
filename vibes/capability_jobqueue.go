package vibes

import (
	"github.com/mgomes/vibescript/internal/runtime"
	"github.com/mgomes/vibescript/vibes/capability/jobqueue"
)

// NewJobQueueCapability constructs a job-queue CapabilityAdapter bound
// to the provided script-facing name. The adapter wraps a
// *jobqueue.Capability built from impl and dispatches the enqueue
// builtin (and the retry builtin when impl satisfies
// jobqueue.JobQueueWithRetry).
func NewJobQueueCapability(name string, impl jobqueue.JobQueue) (CapabilityAdapter, error) {
	return runtime.NewJobQueueCapability(name, impl)
}

// MustNewJobQueueCapability constructs a job-queue CapabilityAdapter or
// panics when name is empty or impl is a nil implementation.
func MustNewJobQueueCapability(name string, impl jobqueue.JobQueue) CapabilityAdapter {
	adapter, err := NewJobQueueCapability(name, impl)
	if err != nil {
		panic(err)
	}
	return adapter
}
