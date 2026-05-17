package contextcap_test

import (
	"context"
	"fmt"

	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/capability/contextcap"
	"github.com/mgomes/vibescript/vibes/value"
)

// stubResolver returns a fixed object exposing a player id under the
// "user" key. Real embedders would pull values out of ctx.
func stubResolver(_ context.Context) (value.Value, error) {
	return value.NewObject(map[string]value.Value{
		"user": value.NewObject(map[string]value.Value{
			"id": value.NewString("player-1"),
		}),
	}), nil
}

func ExampleNewCapability() {
	cap, err := contextcap.NewCapability("ctx", stubResolver)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(cap.Name())
	// Output: ctx
}

func ExampleMustNewCapability() {
	cap := contextcap.MustNewCapability("ctx", stubResolver)
	fmt.Println(cap.Name())
	// Output: ctx
}

// Example shows how to install a contextcap.Capability through the vibes
// facade and read its data-only resolver value from a script.
func Example() {
	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile(`def run()
  ctx[:user][:id]
end`)
	if err != nil {
		fmt.Println("compile:", err)
		return
	}

	result, err := script.Call(context.Background(), "run", nil, vibes.CallOptions{
		Capabilities: []vibes.CapabilityAdapter{
			vibes.MustNewContextCapability("ctx", stubResolver),
		},
	})
	if err != nil {
		fmt.Println("call:", err)
		return
	}
	fmt.Println(result.String())
	// Output: player-1
}
