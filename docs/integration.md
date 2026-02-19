# Integrating VibeScript in Go

The interpreter runs entirely in Go. Create an engine, compile scripts, and
call functions like so:

```go
package main

import (
    "context"
    "fmt"

    "github.com/mgomes/vibescript/vibes"
)

func main() {
    engine, err := vibes.NewEngine(vibes.Config{})
    if err != nil {
        panic(err)
    }

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
The `require` builtin returns an object containing the module's public
functions and also defines any non-conflicting public functions on the global
scope for convenient access.

```go
engine, err := vibes.NewEngine(vibes.Config{ModulePaths: []string{"/app/workflows"}})
if err != nil {
    panic(err)
}

script, err := engine.Compile(`def total(amount)
  require("fees", as: "helpers")
  helpers.apply_fee(amount)
end`)
```

The interpreter searches each configured directory for `<module>.vibe` in order
and caches compiled modules so subsequent calls to `require` are inexpensive.
For long-running hosts, call `engine.ClearModuleCache()` between runs when
module sources can change.
When a circular module dependency is detected, the runtime reports a concise
chain (for example `a -> b -> a`).
Use the optional `as:` keyword to bind the loaded module object to a global
alias.
Inside a module, use explicit relative paths (`./` or `../`) to load siblings
or parent-local helpers. Relative requires are resolved from the calling
module's directory and are rejected if they escape the module root. Functions
can be exported explicitly with `export def ...`; if no explicit exports are
declared, public names are exported by default and names starting with `_`
remain private. Exported names are only injected into globals when no binding
already exists, so existing host/script globals keep precedence.

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

cap, err := vibes.NewJobQueueCapability("jobs", jobQueue{})
if err != nil {
    panic(err)
}

result, err := script.Call(ctx, "queue_recalc", nil, vibes.CallOptions{
    Capabilities: []vibes.CapabilityAdapter{cap},
})

```

Adapters receive the invocation `context.Context`, making it straightforward to
apply deadlines, tracing spans, or other host-specific policy without hand
wiring builtins.

### First-Party Capability Helpers

VibeScript ships capability helpers for common integration points:

- `NewDBCapability(name, db)` for `find/query/update/sum/each`.
- `NewEventsCapability(name, publisher)` for `publish`.
- `NewJobQueueCapability(name, queue)` for `enqueue/retry`.
- `NewContextCapability(name, resolver)` for data-only request metadata.

```go
dbCap := vibes.MustNewDBCapability("db", myDB)
eventsCap := vibes.MustNewEventsCapability("events", myEvents)
jobsCap := vibes.MustNewJobQueueCapability("jobs", myJobs)
ctxCap := vibes.MustNewContextCapability("ctx", func(ctx context.Context) (vibes.Value, error) {
    userID, _ := ctx.Value("user_id").(string)
    role, _ := ctx.Value("role").(string)
    return vibes.NewObject(map[string]vibes.Value{
        "user": vibes.NewObject(map[string]vibes.Value{
            "id":   vibes.NewString(userID),
            "role": vibes.NewString(role),
        }),
    }), nil
})

result, err := script.Call(ctx, "run", args, vibes.CallOptions{
    Capabilities: []vibes.CapabilityAdapter{dbCap, eventsCap, jobsCap, ctxCap},
})
```

Capability method names follow `capability.method` naming (`db.find`,
`events.publish`, `jobs.enqueue`) so contracts and runtime errors are explicit
about the boundary being enforced.

### Capability Workflow Pattern

A practical pattern is `query -> transform -> publish/enqueue` in one script
call:

1. Query records through `db.each` or `db.query`.
2. Build a data-only payload in script code.
3. Publish notifications via `events.publish` or queue work via `jobs.enqueue`.

This keeps business logic in VibeScript while side effects stay behind typed
host adapters.

### Capability Failure Handling

Capability adapters enforce data-only boundaries and argument shapes at runtime:

- If script args/kwargs are invalid, the call fails before host code executes.
- If host returns callable values, return contracts reject them.
- Adapter errors are surfaced as runtime errors with call-site stack frames.

In host code, handle script call errors the same way as other runtime failures
and log adapter-specific method names from the error text (for example
`db.update attributes must be data-only`).

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
