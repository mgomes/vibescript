# VibeScript

The `vibes/` directory contains the public Go package that hosts the VibeScript interpreter. Downstream applications can depend on it via:

```go
import "vibescript/vibes"
```

### Quick Start Example

```go
package main

import (
    "context"
    "fmt"

    "vibescript/vibes"
)

func main() {
    engine := vibes.NewEngine(vibes.Config{})

    script, err := engine.Compile(`
    def total_with_fee(amount)
      amount + 1
    end
    `)
    if err != nil {
        panic(err)
    }

    result, err := script.Call(
        context.Background(),
        "total_with_fee",
        []vibes.Value{vibes.NewInt(99)},
        vibes.CallOptions{},
    )
    if err != nil {
        panic(err)
    }

    fmt.Println("total:", result.Int())
}
```

Save the script as `.vibe` or embed it in your application. Expose host capabilities by providing values in `CallOptions.Globals` before invoking functions.

## Examples

Representative `.vibe` programs now live under `examples/` and remain grouped by feature area for quick lookup:

- `examples/basics/` – literals, arithmetic, and simple function composition.
- `examples/collections/` – array, hash, and symbol usage including mutation and lookups.
- `examples/control_flow/` – conditionals and recursion examples.
- `examples/blocks/` – block-friendly transformations (map/select/reduce) over collections.
- `examples/hashes/` – symbol-keyed hash manipulation, merging, and reporting helpers.
- `examples/loops/` – range iteration, collection loops, and accumulation helpers.
- `examples/ranges/` – range literals, ascending/descending iteration, and filtered collection helpers.
- `examples/money/` – exercises for the `money` and `money_cents` built-ins.
- `examples/durations/` – duration literals and derived values in seconds.
- `examples/errors/` – patterns that rely on `assert` for validation.
- `examples/capabilities/` – samples that touch `ctx`, `db`, and other declared capabilities.
- `examples/background/` – jobs and events workflows that will pass once those effects are wired in.
- `examples/policies/` – authorization helpers consulted by manifest policies.
- `examples/future/` – stretch goals that require planned language features (blocks, iteration, effectful DB streaming).

Some scripts (notably in `examples/background/` and `examples/future/`) require features that are not yet implemented. Keeping them in the tree makes it easy to track interpreter progress—enable them one by one as functionality lands.

## Documentation

Long-form guides live in `docs/`:

- `docs/introduction.md` – overview and table of contents.
- `docs/arrays.md` – array helpers including map/select/reduce, first/last, push/pop, sum, and set-like operations.
- `docs/hashes.md` – symbol-keyed hashes, merge, and iteration helpers.
- `docs/control-flow.md` – conditionals, loops, and ranges.
- `docs/blocks.md` – working with block literals for enumerable-style operations.
- `docs/integration.md` – integrating the interpreter in Go applications.
- `docs/examples/` – runnable scenario guides (campaign reporting, rewards, notifications, and more).

## Development

- Run the full test suite with `just test`, which fixes a local Go build cache and executes `go test ./...`.
