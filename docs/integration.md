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
values (hashes, builtins, arrays) or, for richer integrations, by supplying
typed adapters via `CallOptions.Capabilities`. Review
`examples/capabilities/` and the test harness in `vibes/examples_test.go` for
mocks you can repurpose.

### Module Search Paths

Set `Config.ModulePaths` to the directories that contain re-usable `.vibe`
files. Scripts can then call `require("module_name")` to load another file.
The `require` builtin returns an object containing the module's functions and
also defines any non-conflicting functions on the global scope for convenient
access.

```go
engine := vibes.NewEngine(vibes.Config{ModulePaths: []string{"/app/workflows"}})

script, err := engine.Compile(`def total(amount)
  helpers = require("fees")
  helpers.apply_fee(amount)
end`)
```

The interpreter searches each configured directory for `<module>.vibe` and
caches compiled modules so subsequent calls to `require` are inexpensive.

### Capability Adapters

Use `CallOptions.Capabilities` to install first-class, typed integrations. The
`vibes.NewJobQueueCapability` helper wraps a host `JobQueue` implementation and
exposes `enqueue` (and `retry` when supported) with automatic argument parsing
and context propagation.

```go
type jobQueue struct{}

func (jobQueue) Enqueue(ctx context.Context, job vibes.JobQueueJob) (vibes.Value, error) {
    log.Printf("queue %s with payload %+v", job.Name, job.Payload)
    return vibes.NewString("queued"), nil
}

cap := vibes.NewJobQueueCapability("jobs", jobQueue{})

result, err := script.Call(ctx, "queue_recalc", nil, vibes.CallOptions{
    Capabilities: []vibes.CapabilityAdapter{cap},
})

```

Adapters receive the invocation `context.Context`, making it straightforward to
apply deadlines, tracing spans, or other host-specific policy without hand
wiring builtins.

### Handling Dynamic Types

Every call returns a `vibes.Value`. Inspect the `Kind()` before consuming it:

```go
result, err := script.Call(ctx, "handler", args, vibes.CallOptions{})
if err != nil {
    return err
}

switch result.Kind() {
case vibes.KindInt:
    fmt.Println("int:", result.Int())
case vibes.KindHash:
    fmt.Println("hash keys:", result.Hash())
case vibes.KindNil:
    // nothing returned
default:
    return fmt.Errorf("unexpected return type: %v", result.Kind())
}
```

Because the interpreter is dynamic, there is no compile-time guarantee about
return values—always branch on `Kind()` when you need type safety.

### Error Handling and Stack Traces

Runtime errors arrive as `*vibes.RuntimeError`, which includes a stack trace
with line and column information for debugging. Use `errors.As()` to check for
runtime errors:

```go
result, err := script.Call(ctx, "process", args, opts)
if err != nil {
    var rtErr *vibes.RuntimeError
    if errors.As(err, &rtErr) {
        // Runtime error with stack trace
        log.Printf("Script error: %v", rtErr)
        // Access individual frames if needed
        for _, frame := range rtErr.Frames {
            log.Printf("  %s at %d:%d", frame.Function, frame.Pos.Line, frame.Pos.Column)
        }
    } else {
        // Other error (compilation, etc.)
        return err
    }
}
```

Example error output:

```
assertion failed: amount must be positive
  at validate_amount (3:7)
  at validate_amount (8:3)
  at process_payment (8:3)
  at process_payment (12:5)
```

The first frame shows where the error occurred, followed by the call stack
showing where each function was called from.
