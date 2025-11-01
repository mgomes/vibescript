# VibeScript

The `vibes/` directory contains the public Go package that hosts the VibeScript interpreter. Downstream applications can depend on it via:

```go
import "vibescript/vibes"
```

## Examples

Representative `.vibe` programs now live under `examples/` and remain grouped by feature area for quick lookup:

- `examples/basics/` – literals, arithmetic, and simple function composition.
- `examples/collections/` – array, hash, and symbol usage including mutation and lookups.
- `examples/control_flow/` – conditionals and recursion examples.
- `examples/blocks/` – block-friendly transformations (map/select/reduce) over collections.
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

## Development

- Run the full test suite with `just test`, which fixes a local Go build cache and executes `go test ./...`.
