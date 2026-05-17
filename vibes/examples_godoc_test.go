package vibes_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/value"
)

func ExampleNewEngine() {
	engine, err := vibes.NewEngine(vibes.Config{StepQuota: 50_000})
	if err != nil {
		panic(err)
	}
	_ = engine
	fmt.Println("engine ready")
	// Output: engine ready
}

func ExampleMustNewEngine() {
	engine := vibes.MustNewEngine(vibes.Config{StepQuota: 50_000})
	_ = engine
	fmt.Println("engine ready")
	// Output: engine ready
}

// Example_simpleScript shows the full compile-and-call cycle a host
// embedder uses to drive a Vibescript program.
func Example_simpleScript() {
	engine := vibes.MustNewEngine(vibes.Config{StepQuota: 50_000})
	script, err := engine.Compile(`def greet(name)
  "hello " + name
end`)
	if err != nil {
		panic(err)
	}
	result, err := script.Call(
		context.Background(),
		"greet",
		[]value.Value{value.NewString("world")},
		vibes.CallOptions{},
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(result.String())
	// Output: hello world
}

func ExampleEngine_Compile() {
	engine := vibes.MustNewEngine(vibes.Config{StepQuota: 50_000})
	script, err := engine.Compile(`def answer()
  42
end`)
	if err != nil {
		panic(err)
	}
	fn, ok := script.Function("answer")
	if !ok {
		panic("answer function not found")
	}
	fmt.Println(fn.Name)
	// Output: answer
}

// ExampleEngine_Execute parses a source string under the active limits
// without retaining the compiled script; useful as a syntax check.
func ExampleEngine_Execute() {
	engine := vibes.MustNewEngine(vibes.Config{StepQuota: 50_000})
	if err := engine.Execute(context.Background(), `def noop()
end`); err != nil {
		panic(err)
	}
	fmt.Println("compiled ok")
	// Output: compiled ok
}

func ExampleScript_Call() {
	engine := vibes.MustNewEngine(vibes.Config{StepQuota: 50_000})
	script, err := engine.Compile(`def add(a, b)
  a + b
end`)
	if err != nil {
		panic(err)
	}
	result, err := script.Call(
		context.Background(),
		"add",
		[]value.Value{value.NewInt(2), value.NewInt(3)},
		vibes.CallOptions{},
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(result.String())
	// Output: 5
}

// ExampleConfig_strictEffects shows that strict-effects mode rejects
// callable globals so hosts can be sure side effects flow through
// declared capabilities.
func ExampleConfig_strictEffects() {
	engine := vibes.MustNewEngine(vibes.Config{StrictEffects: true})
	script, err := engine.Compile(`def run()
  notify("hi")
end`)
	if err != nil {
		panic(err)
	}
	_, err = script.Call(
		context.Background(),
		"run",
		nil,
		vibes.CallOptions{
			Globals: map[string]value.Value{
				"notify": vibes.NewBuiltin("notify", func(_ *vibes.Execution, _ value.Value, _ []value.Value, _ map[string]value.Value, _ value.Value) (value.Value, error) {
					return value.NewNil(), nil
				}),
			},
		},
	)
	if err == nil {
		fmt.Println("expected strict-effects rejection")
		return
	}
	if strings.Contains(err.Error(), "strict effects") {
		fmt.Println("rejected callable global")
		return
	}
	fmt.Println("unexpected error:", err)
	// Output: rejected callable global
}

func ExampleNewBuiltin() {
	engine := vibes.MustNewEngine(vibes.Config{StepQuota: 50_000})
	script, err := engine.Compile(`def shout(word)
  upcase(word)
end`)
	if err != nil {
		panic(err)
	}
	upcase := vibes.NewBuiltin("upcase", func(_ *vibes.Execution, _ value.Value, args []value.Value, _ map[string]value.Value, _ value.Value) (value.Value, error) {
		return value.NewString(strings.ToUpper(args[0].String())), nil
	})
	result, err := script.Call(
		context.Background(),
		"shout",
		[]value.Value{value.NewString("hi")},
		vibes.CallOptions{
			Globals: map[string]value.Value{"upcase": upcase},
		},
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(result.String())
	// Output: HI
}

func ExampleNewAutoBuiltin() {
	engine := vibes.MustNewEngine(vibes.Config{StepQuota: 50_000})
	script, err := engine.Compile(`def run()
  tenant
end`)
	if err != nil {
		panic(err)
	}
	tenant := vibes.NewAutoBuiltin("tenant", func(_ *vibes.Execution, _ value.Value, _ []value.Value, _ map[string]value.Value, _ value.Value) (value.Value, error) {
		return value.NewString("acme"), nil
	})
	result, err := script.Call(
		context.Background(),
		"run",
		nil,
		vibes.CallOptions{
			Globals: map[string]value.Value{"tenant": tenant},
		},
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(result.String())
	// Output: acme
}
