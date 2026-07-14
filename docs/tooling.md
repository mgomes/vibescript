# Tooling Commands

The `vibes` CLI provides a small set of stable tooling commands for local
development and CI.

## Help and command syntax

Run `vibes --help` for the command list or `vibes help <command>` for complete
command-specific flags. Successful help is written to stdout and exits with
status 0. Running `vibes` without a command or with an unknown command writes
usage and an error to stderr and exits non-zero.

After a subcommand, flags accept one or two leading hyphens, must appear before
the first positional argument, and may be terminated explicitly with `--`.
The root command does not use `--` to escape command selection. For `vibes
run`, every token after the script path is passed to the script as a string,
including tokens that begin with `-`. `vibes check` and
`vibes analyze` accept exactly one script path; `vibes lsp` and `vibes repl`
accept no positional arguments.

## `vibes run [options] <script> [args...]`

Compiles and executes a script file.
Use `vibes run [options] -e <snippet>` for the inline form without a script
path.

```bash
vibes run ./examples/strings/operations.vibe
```

Useful flags:

- `-function <name>`: invoke a specific function. Without this flag, the CLI
  executes top-level statements when present and otherwise invokes `run`.
- `-check`: compile only, without executing.
- `-module-path <dir>`: add module search paths for `require`.
- `-e '<snippet>'`: evaluate an inline snippet without a script file.
- `-watch`: re-run the script whenever it or its modules change.
- `-profile <name>`: select a quota profile (see below; default `xhigh`).
- `-step-quota` / `-memory-quota` / `-recursion-limit <n>`: override a single
  profile quota (`-1` = unlimited; memory quota is in bytes).

### Quota profiles

Every execution runs under three quotas — a **step** quota (aborts runaway
loops), a **memory** quota (bounds retained heap, enforced by the reachable-graph
accounting), and a **recursion** limit (bounds call depth). The CLI selects a
coherent bundle of all three with `-profile`:

| Profile  | Step quota    | Memory quota | Recursion | Intended use                                  |
| -------- | ------------- | ------------ | --------- | --------------------------------------------- |
| `low`    | 1,000,000     | 16 MiB       | 256       | tight, untrusted embedded budget              |
| `medium` | 20,000,000    | 128 MiB      | 1,000     | moderate embedded budget                      |
| `high`   | 200,000,000   | 512 MiB      | 4,000     | generous embedded budget                      |
| `xhigh`  | unlimited     | unlimited    | 10,000    | run like a normal interpreter (CLI default)   |

`vibes run`, `vibes test`, and `vibes repl` default to **`xhigh`**: the CLI
runs your own scripts on your own machine, so it is not a sandbox — steps and
memory are unlimited and only a finite recursion cap remains to catch infinite
recursion. An unlimited memory quota skips the accounting walk entirely, so
`xhigh` also avoids the sandbox's per-check cost.

Layer `-step-quota`, `-memory-quota`, or `-recursion-limit` on top of a profile
to override just that quota; unset overrides keep the profile's value. To
measure or reproduce sandbox behaviour, for example, run under `xhigh` but with
a real memory cap:

```bash
vibes run -memory-quota=$((512 * 1024 * 1024)) ./script.vibe
```

Hosts embedding the engine select the same bundles through the `vibes`
package (`vibes.ProfileLow`/`Medium`/`High`/`XHigh`, `vibes.QuotaProfileByName`);
see [Integrating Vibescript in Go](integration.md#quota-profiles).

### Inline evaluation (`-e`)

```bash
vibes run -e '1 + 2'
```

The snippet is compiled with an implicit zero-argument entrypoint (the same
mechanism the REPL uses). It may contain multiple statements and top-level
definitions; executable top-level statements run through that entrypoint.
Module paths default to the current working directory plus any `-module-path`
entries, and the result is printed when it is not nil. `-e` cannot be combined
with `-function`, `-watch`, or positional arguments.

### Watch mode (`-watch`)

```bash
vibes run -watch ./examples/strings/operations.vibe
```

Runs the script immediately, then re-runs it whenever the script file or
any `.vibe` file under its module directories changes (nested module
paths like `require "billing/fees"` are watched too). Compile and runtime
errors are printed without ending the watch, so you can fix the file and
save again. Press `ctrl-c` to stop.

### Result rendering limit

A script's non-nil return value is rendered and printed after the run
finishes. Because the runtime call has already returned by then, the
rendering path is outside the interpreter's step and memory quotas. To keep
a script that returns a huge nested array or hash from forcing the CLI to
allocate the whole formatted string in host memory, rendering stops once the
output would exceed 1 MiB and the command fails with
`result rendering exceeds 1048576 bytes` instead of printing a truncated
value. Reduce the returned value or have the script stream output itself.
The cap matches the other stdlib output guards (see
[Runtime Sandbox & Limits](../README.md#runtime-sandbox--limits)).

## `vibes check [options] <script>`

Compiles a script and reports every statically checkable contract issue —
across all functions, class methods, and top-level code — without executing
anything. It applies the same semantic contract as `vibes run -check`
(ADR-004): locals take the types of the expressions assigned to them,
annotations are compile-time facts, known contradictions are errors, and
unknown values are always permitted and left to the runtime checks.

Whole-script checks follow the entrypoint's execution order. Top-level code is
checked statement by statement, and a `require` binds its module's exports at
the point it runs, so a call before a `require` neither resolves nor validates
that module's contracts. Functions the top-level code calls are checked under
the runtime state at each call site; every other function is checked as if the
entrypoint had completed, with all of its top-level requires loaded.

```bash
vibes check ./examples/strings/operations.vibe
```

Flags:

- `-module-path <dir>`: add module search paths for `require` (repeatable).

Issues print one per line as `path:line:column: message (function)` and the
exit code is non-zero when any issue is found, so the command slots directly
into CI and deployment gates. Scripts relying on host-injected globals or
capabilities report them as undefined here, because the CLI checks without a
host context; embedding hosts get the same checks with their bindings applied
through the `CheckWarnings*` API.

## `vibes fmt [options] <path>...`

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

## `vibes test [options] [path...]`

Discovers `*_test.vibe` files and runs their test functions.

```bash
vibes test ./tests
vibes test -run 'pricing' ./tests
vibes test ./tests/billing_test.vibe
```

A test is a function whose name starts with `test_` and takes no required
parameters; it fails when it raises or an `assert` inside it fails. Failures
are reported with the assertion message and source position. Flags:

- `-run <regexp>`: run only test functions whose name matches.
- `-module-path <dir>`: add module search paths for `require` (each test
  file's own directory is always included).
- `-profile <name>`: select the execution quota profile (default `xhigh`).
- `-step-quota`, `-memory-quota`, and `-recursion-limit <n>`: override one
  profile quota (`-1` = unlimited).

The exit code is non-zero when any test fails, so the command slots directly
into CI.

## Source-size limits

The commands that compile a script file (`vibes run`, `vibes check`,
`vibes analyze`, and `vibes test`) stat each file and reject inputs larger than the engine's
source-size limit *before* reading the file into memory. This mirrors how
`require` guards module loading, so an oversized file fails fast with
`source exceeds maximum size (<size> > <limit> bytes)` instead of being read
in full and then rejected by the parser. The limit defaults to 1 MiB and is
configured through `Config.MaxSourceBytes` when embedding the engine.

## `vibes lsp`

Starts an LSP prototype over stdio, with hover, completion, and diagnostics.

```bash
vibes lsp
```

This command is meant to be launched by your editor's language-server client.
It currently tracks in-memory document updates from `didOpen`/`didChange` and
releases a document's state on `didClose`. Editor setup and the full
feature/limitation list are documented in [lsp.md](lsp.md).

## `vibes repl`

Starts the interactive REPL for quick evaluation.

```bash
vibes repl
```

The REPL accepts `-profile`, `-step-quota`, `-memory-quota`, and
`-recursion-limit` with the same semantics as `vibes run`.

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

CI enforces these gates: the `Benchmark smoke gates` step in
`.github/workflows/benchmarks.yml` runs the smoke check on every pull request
and push to `master`, and a threshold breach fails the workflow (the
remaining benchmark and artifact steps are skipped in that case). When the
gate passes, the run uploads the raw results and the baseline trend
comparison as workflow artifacts. Artifacts expire with the repository's
retention window, so they are a recent-run comparison aid, not durable
history — long-lived reference points belong in `benchmarks/baselines/`.

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
