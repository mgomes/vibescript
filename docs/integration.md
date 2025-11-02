# Integrating VibeScript in Go

The interpreter runs entirely in Go. Create an engine, compile scripts, and
call functions like so:

```go
package main

import (
    "context"
    "fmt"

    "vibescript/vibes"
)

func main() {
    engine := vibes.NewEngine(vibes.Config{})

    scriptSource := `
    def total_with_bonus(base, bonus)
      base + bonus
    end
    `

    script, err := engine.Compile(scriptSource)
    if err != nil {
        panic(err)
    }

    result, err := script.Call(
        context.Background(),
        "total_with_bonus",
        []vibes.Value{vibes.NewInt(100), vibes.NewInt(25)},
        vibes.CallOptions{},
    )
    if err != nil {
        panic(err)
    }

    fmt.Println("result:", result.Int())
}
```

Host applications can expose capabilities by seeding `CallOptions.Globals` with
values (hashes, builtins, arrays) before invoking script functions. Review
`examples/capabilities/` and the test harness in `vibes/examples_test.go` for
mocks you can repurpose.
