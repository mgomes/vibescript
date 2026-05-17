package jobqueue_test

import (
	"context"
	"fmt"

	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/capability/jobqueue"
	"github.com/mgomes/vibescript/vibes/value"
)

// stubQueue records enqueue calls for the godoc Examples. Real embedders
// would push work onto Redis, SQS, or another backing store.
type stubQueue struct{}

func (stubQueue) Enqueue(_ context.Context, job jobqueue.JobQueueJob) (value.Value, error) {
	return value.NewString(job.Name), nil
}

func ExampleNewCapability() {
	cap, err := jobqueue.NewCapability("jobs", stubQueue{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(cap.Name, cap.HasRetry())
	// Output: jobs false
}

func ExampleMustNewCapability() {
	cap := jobqueue.MustNewCapability("jobs", stubQueue{})
	fmt.Println(cap.Name)
	// Output: jobs
}

// Example shows how to wire a jobqueue.Capability into a script
// invocation via the vibes facade and observe the enqueue dispatch.
func Example() {
	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile(`def run()
  jobs.enqueue("send_email", { to: "alex@example.com" })
end`)
	if err != nil {
		fmt.Println("compile:", err)
		return
	}

	result, err := script.Call(context.Background(), "run", nil, vibes.CallOptions{
		Capabilities: []vibes.CapabilityAdapter{
			vibes.MustNewJobQueueCapability("jobs", stubQueue{}),
		},
	})
	if err != nil {
		fmt.Println("call:", err)
		return
	}
	fmt.Println(result.String())
	// Output: send_email
}
