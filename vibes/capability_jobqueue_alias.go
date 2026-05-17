package vibes

import (
	"github.com/mgomes/vibescript/internal/runtime"
	"github.com/mgomes/vibescript/vibes/capability/jobqueue"
)

// Type aliases for the job-queue capability types that moved to
// vibes/capability/jobqueue. These keep embedders that import vibes
// source-compatible during the transition. Removed in v0.29.0.
type (
	JobQueue               = jobqueue.JobQueue
	JobQueueWithRetry      = jobqueue.JobQueueWithRetry
	JobQueueJob            = jobqueue.JobQueueJob
	JobQueueEnqueueOptions = jobqueue.JobQueueEnqueueOptions
	JobQueueRetryRequest   = jobqueue.JobQueueRetryRequest
)

// NewJobQueueCapability constructs a CapabilityAdapter bound to the
// provided name. The returned adapter delegates to a *jobqueue.Capability.
func NewJobQueueCapability(name string, impl JobQueue) (CapabilityAdapter, error) {
	return runtime.NewJobQueueCapability(name, impl)
}

// MustNewJobQueueCapability is the panicking variant of NewJobQueueCapability.
func MustNewJobQueueCapability(name string, impl JobQueue) CapabilityAdapter {
	adapter, err := NewJobQueueCapability(name, impl)
	if err != nil {
		panic(err)
	}
	return adapter
}
