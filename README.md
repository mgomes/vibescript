# Vibescript

As [vibe coding](https://en.wikipedia.org/wiki/Vibe_coding) grows in popularity, there will be many domains where we need to narrow what users can build. Instead of giving them a blank canvas, we can offer an opinionated set of well-defined primitives that combine into predictable, safe applications. Think of it less like traditional software development and more like [HyperCard](https://en.wikipedia.org/wiki/HyperCard): flexible, but within bounds.

Even in these constrained environments, non-technical users still need a way to express custom logic. That’s where Vibescript comes in. It’s a Ruby-like scripting language designed to be easy to read, and easy for AI to vibe code. The interpreter is written in Go and can be embedded directly into any Go application.

**Key Features**

- Ruby-like syntax: blocks, ranges, zero-paren defs, symbol hashes.
- Gradual typing: optional annotations, nullable `?`, positional/keyword args, return checks.
- Time & Duration helpers: literals, math, offsets (`ago`/`after`), Go-layout `format`.
- Money type and helpers.
- Embeddable in Go with capabilities and `require`-style modules.
- Interactive REPL with history, autocomplete, help/vars panels.

```vibe
# Quick leaderboard report with typing, time math, and blocks
def leaderboard(players: array, since: time? = nil, limit: int = 5) -> array
  cutoff = since || 7.days.ago(Time.now)
  recent = players.select do |p|
    Time.parse(p[:last_seen]) >= cutoff
  end

  recent
    .map { |p| { name: p[:name], score: p[:score], last_seen: Time.parse(p[:last_seen]) } }
    .sort do |a, b|
      b[:score] <=> a[:score]
    end
    .first(limit)
    .map do |entry|
      {
        name: entry[:name],
        score: entry[:score],
        last_seen: entry[:last_seen].format("2006-01-02 15:04:05"),
      }
    end
end
```

### Quick Start Example

> [!WARNING]
> This project is in active development. Expect breaking changes until it reaches a tagged 1.0 release.

```go
package main

import (
    "context"
    "fmt"

    "github.com/mgomes/vibescript/vibes"
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

Scripts can live in `.vibe` files or be embedded inline. Host applications expose capabilities by seeding `CallOptions.Globals` or registering typed adapters through `CallOptions.Capabilities` before invoking functions.

### Interactive REPL

The `vibes` CLI includes an interactive REPL for experimenting with the language:

```bash
vibes repl
```

The REPL maintains a persistent environment, so variables assigned in one expression are available in subsequent ones. It also provides command history (navigate with up/down arrows) and tab completion for built-in functions, keywords, and defined variables.

#### Commands

| Command   | Description              |
|-----------|--------------------------|
| `:help`   | Toggle help panel        |
| `:vars`   | Toggle variables panel   |
| `:clear`  | Clear output history     |
| `:reset`  | Reset the environment    |
| `:quit`   | Exit the REPL            |

#### Keyboard Shortcuts

| Key       | Action                   |
|-----------|--------------------------|
| `ctrl+k`  | Toggle help              |
| `ctrl+v`  | Toggle variables panel   |
| `ctrl+l`  | Clear history            |
| `ctrl+c`  | Quit                     |
| `Tab`     | Autocomplete             |

## Examples

Representative `.vibe` programs are grouped under `examples/`:

- `examples/basics/` – literals, arithmetic, and simple function composition.
- `examples/collections/` – array, hash, and symbol usage including mutation and lookups.
- `examples/control_flow/` – conditionals and recursion examples.
- `examples/blocks/` – block-friendly transformations (map/select/reduce) over collections.
- `examples/hashes/` – symbol-keyed hash manipulation, merging, and reporting helpers.
- `examples/loops/` – range iteration, collection loops, and accumulation helpers.
- `examples/ranges/` – range literals, ascending/descending iteration, and filtered collection helpers.
- `examples/money/` – exercises for the `money` and `money_cents` built-ins.
- `examples/durations/` – duration literals, math (add/sub/mul/div/mod), and time offsets.
- `examples/time/` – Time creation, formatting (Go layouts), and duration/time math.
- `examples/errors/` – patterns that rely on `assert` for validation.
- `examples/capabilities/` – samples that touch `ctx`, `db`, and other declared capabilities.
- `examples/background/` – jobs and events workflows that land as host integrations mature.
- `examples/policies/` – authorization helpers consulted by manifest policies.
- `examples/future/` – stretch goals for planned language features.

Some scripts (notably in `examples/background/` and `examples/future/`) reference features that are still under development; they remain in the tree to track interpreter progress.

## Documentation

Long-form guides live in `docs/`:

- `docs/introduction.md` – overview and table of contents.
- `docs/arrays.md` – array helpers including map/select/reduce, first/last, push/pop, sum, and set-like operations.
- `docs/hashes.md` – symbol-keyed hashes, merge, and iteration helpers.
- `docs/control-flow.md` – conditionals, loops, and ranges.
- `docs/blocks.md` – working with block literals for enumerable-style operations.
- `docs/integration.md` – integrating the interpreter in Go applications.
- `docs/durations.md` – duration literals, conversions, and arithmetic.
- `docs/time.md` – Time creation, formatting with Go layouts, accessors, and time/duration math.
- `docs/typing.md` – gradual typing: annotations, nullable `?`, positional/keyword binding, and return checks.
- `docs/examples/` – runnable scenario guides (campaign reporting, rewards, notifications, module usage, and more).
- `docs/releasing.md` – GoReleaser workflow for changelog and GitHub release automation.

## Development

This repository uses [Just](https://github.com/casey/just) for common tasks:

- `just test` runs the full Go test suite (`go test ./...`).
- `just lint` checks formatting (`gofmt`) and runs `golangci-lint` with a generous timeout.
- Add new recipes in the `Justfile` as workflows grow.

Contributions should run `just test` and `just lint` (or the equivalent `go` and `golangci-lint` commands) before submitting patches.
