# Tooling Commands

The `vibes` CLI provides a small set of stable tooling commands for local
development and CI.

## `vibes run <script>`

Compiles and executes a script file.

```bash
vibes run ./examples/strings/operations.vibe
```

Useful flags:

- `-function <name>`: invoke a specific function (default `run`).
- `-check`: compile only, without executing.
- `-module-path <dir>`: add module search paths for `require`.

## `vibes fmt <path>`

Applies canonical formatting for `.vibe` files.

```bash
vibes fmt ./examples
vibes fmt -w ./examples
vibes fmt -check .
```

Flags:

- `-w`: write formatted output back to files.
- `-check`: fail when any file would be reformatted.

## `vibes analyze <script>`

Runs script-level lint checks.

```bash
vibes analyze ./examples/strings/operations.vibe
```

Current checks include unreachable statements after terminating operations such
as `return` and `raise`.

## `vibes lsp`

Starts an LSP prototype over stdio, with hover, completion, and diagnostics.

```bash
vibes lsp
```

This command is meant to be launched by your editor's language-server client.
It currently tracks in-memory document updates from `didOpen`/`didChange`.

## `vibes repl`

Starts the interactive REPL for quick evaluation.

```bash
vibes repl
```

REPL command set:

- `:help`, `:vars`, `:globals`, `:functions`, `:types`
- `:last_error`, `:clear`, `:reset`, `:quit`

## Installing the CLI

Use `just install` to install `vibes` into your Go bin directory:

```bash
just install
```

By default this uses `$GOBIN`, or `$GOPATH/bin` when `GOBIN` is unset.
To choose a custom destination:

```bash
just install /usr/local/bin
```

## Benchmark Runner

Use the benchmark runner script for stable local perf baselines.

```bash
scripts/bench_runtime.sh
```

Common options:

- `--pattern '^Benchmark(Execution|Call|Compile|Module|Complex)'`
- `--count 5`
- `--benchtime 2s`
- `--out benchmarks/array_vs_tally.txt`

The script is also wired into `just bench`.

## Versioned Baselines

Release-tracked baseline artifacts live under `benchmarks/baselines/`:

- `v0.20.0-pr.txt` for PR/push benchmark profile.
- `v0.20.0-full.txt` for scheduled full benchmark profile.

Compare a new run against a baseline:

```bash
scripts/bench_compare_baseline.sh benchmarks/baselines/v0.20.0-pr.txt benchmarks/latest.txt
```

## Benchmark Smoke Gates

Use the smoke-check script to catch obvious performance regressions before
running the full suite:

```bash
scripts/bench_smoke_check.sh
```

Thresholds live in `benchmarks/smoke_thresholds.txt` and are checked against
both `ns/op` and `allocs/op`.
The smoke output includes per-benchmark deltas (`actual - threshold`) so CI
summaries show headroom or regression at a glance.

## Scheduled Full Runs

The benchmark workflow runs weekly on Mondays at 06:00 UTC (`cron: 0 6 * * 1`)
using the full profile (`--count 1 --benchtime 2s`).

Each run publishes:

- benchmark results artifact (`.bench/benchmark-full.txt`)
- baseline trend comparison (`.bench/trend-full.txt`)

## Benchmark Interpretation and Triage

Use this triage loop when a smoke gate regresses:

1. Re-run just the failing benchmarks locally with `--count 5` and a longer
   `--benchtime` to confirm the signal.
2. Capture profiles for the failing benchmark(s) with
   `scripts/bench_profile.sh --pattern '<benchmark-regex>'`.
3. Compare `cpu.top.txt` and `mem.top.txt` before/after your change.
4. Fix the hot path first, then rerun smoke checks and full benchmark runs.
5. Update thresholds only when behavior has intentionally changed and the
   new baseline is understood.

## Benchmark Profiling

Capture benchmark CPU/memory profiles plus `pprof` top summaries:

```bash
scripts/bench_profile.sh --pattern '^BenchmarkExecutionArrayPipeline$'
```

Artifacts are written under `benchmarks/profiles/<timestamp>/`:

- `bench.txt`
- `cpu.out`, `cpu.top.txt`
- `mem.out`, `mem.top.txt`
- `meta.txt`

This is also available as:

```bash
just bench-profile
```

## Flamegraphs

Generate flamegraph-style views from captured profiles:

```bash
go tool pprof -http=:0 benchmarks/profiles/<timestamp>/cpu.out
go tool pprof -http=:0 benchmarks/profiles/<timestamp>/mem.out
```

Hotspot checklist:

1. Confirm the top cumulative frames match the regressed benchmark path.
2. Separate CPU-bound hotspots from allocation hotspots.
3. Validate a fix with both `bench_runtime.sh` and `bench_smoke_check.sh`.
4. Keep profile artifacts for before/after comparison in PR notes.

## Performance Playbook

Before merging a perf change:

1. Capture a baseline (`scripts/bench_runtime.sh --count 3`).
2. Apply one optimization at a time.
3. Re-run the affected benchmark subset and smoke checks.
4. Profile if results are unclear or regressions appear.
5. Run `go test ./...` before finalizing changes.
