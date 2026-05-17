package events_test

import (
	"context"
	"fmt"

	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/capability/events"
	"github.com/mgomes/vibescript/vibes/value"
)

// stubPublisher records publish requests for the godoc Examples. Real
// embedders would forward events to their preferred bus (Kafka, NATS, ...).
type stubPublisher struct{}

func (stubPublisher) Publish(_ context.Context, req events.PublishRequest) (value.Value, error) {
	return value.NewHash(map[string]value.Value{
		"topic": value.NewString(req.Topic),
	}), nil
}

func ExampleNewCapability() {
	cap, err := events.NewCapability("events", stubPublisher{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(cap.PublishMethodName())
	// Output: events.publish
}

func ExampleMustNewCapability() {
	cap := events.MustNewCapability("notifications", stubPublisher{})
	fmt.Println(cap.PublishMethodName())
	// Output: notifications.publish
}

// Example shows how to wire an events.Capability into a script invocation
// via the vibes facade and observe the publish dispatch.
func Example() {
	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile(`def run()
  events.publish("orders.created", { id: "o-1" })
end`)
	if err != nil {
		fmt.Println("compile:", err)
		return
	}

	result, err := script.Call(context.Background(), "run", nil, vibes.CallOptions{
		Capabilities: []vibes.CapabilityAdapter{
			vibes.MustNewEventsCapability("events", stubPublisher{}),
		},
	})
	if err != nil {
		fmt.Println("call:", err)
		return
	}
	fmt.Println(result.Hash()["topic"].String())
	// Output: orders.created
}
