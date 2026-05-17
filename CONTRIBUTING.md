# Contributing to Vibescript

Vibescript is a sandboxed, Ruby-like scripting language designed to run
untrusted user scripts inside Go host applications. The interpreter exposes
host functionality through typed *capability adapters* so embedders can decide
exactly what scripts may touch. For a tour of the runtime, parser, and module
system, see [`docs/architecture.md`](docs/architecture.md).

This guide is for drive-by contributors. It covers the local dev loop, what CI
enforces, and the conventions reviewers will look for.

## Dev setup

- **Go 1.26 or newer.** The version is pinned in `go.mod` and CI uses the
  same.
- **[`just`](https://github.com/casey/just)** is recommended; every command
  below has a `just` recipe and the same recipes run in CI.
- **`golangci-lint` v2.** Install with
  `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`.
- **`gofumpt`** is applied through `golangci-lint fmt`; you do not need to
  install it separately, but having it on `$PATH` is convenient for editor
  integrations.

Clone, then verify the tree is healthy:

```bash
just test
just lint
```

## Building and running

```bash
just install            # installs `vibes` into $GOBIN (or $GOPATH/bin)
vibes run examples/control_flow/case_when.vibe
vibes repl              # interactive REPL
```

`just install /usr/local/bin` (or any directory) installs to a custom location.

## Testing

The project uses the standard library `testing` package exclusively. There is
no testify, no ginkgo, no custom assertion framework. For diffing complex
values, use [`github.com/google/go-cmp/cmp`](https://pkg.go.dev/github.com/google/go-cmp/cmp).

```bash
just test                       # go test ./...
just test-race                  # go test -race ./...
just fuzz                       # 10s per fuzz target (default)
just fuzz fuzztime=30s          # bump per-target fuzz time
just bench                      # runtime benchmarks
```

### Table-driven test template

New tests should follow the table-driven pattern that Wave 4 PRs are landing
across the codebase:

```go
func TestFoo(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name    string
        source  string
        fn      string
        args    []vibes.Value
        want    vibes.Value
        wantErr string
    }{
        {
            name:   "adds two ints",
            source: `def add(a, b); a + b; end`,
            fn:     "add",
            args:   []vibes.Value{vibes.NewInt(2), vibes.NewInt(3)},
            want:   vibes.NewInt(5),
        },
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            t.Parallel()
            // exercise + assert
        })
    }
}
```

Notes:

- Call `t.Parallel()` in both the parent test and each subtest unless shared
  state forbids it.
- Use `cmp.Diff` for non-trivial assertions and report the diff in `t.Errorf`.
- Helpers belong in `_test.go` files; see
  `vibes/capability_test_helpers_test.go` for the shared compile/call
  utilities.

## Linting and formatting

`just lint` runs the same two checks CI runs:

```bash
golangci-lint fmt --diff        # formatter check (gofmt + gofumpt)
golangci-lint run --timeout=10m # enabled linters from .golangci.yml
```

`just lint-fix` auto-applies the formatter and `--fix` for fixable lints.

CI additionally runs `vibes fmt -check .` against every `.vibe` file in the
repo and `./scripts/check_vibe_zero_arg_parens.sh` to enforce zero-arg
paren style. If you touch a `.vibe` fixture, run `vibes fmt -w <path>` before
committing.

The lint contract lives in `.golangci.yml`. Enabled linters: `errcheck`,
`govet`, `staticcheck`, `ineffassign`, `unused`, `misspell`. Format with
`gofmt` + `gofumpt` (extra rules on).

## Adding a capability adapter

Capabilities are the supported extension point for exposing host
functionality to scripts. The general checklist:

1. Define the host interface in `vibes/` (e.g. `JobQueue`, `Database`).
2. Provide constructors named `NewXxxCapability(...) (CapabilityAdapter, error)`
   and `MustNewXxxCapability(...) CapabilityAdapter`. Follow the existing
   pattern in `vibes/capability_jobqueue.go`.
3. Register the capability's contract through the capability contract scanner
   (`vibes/capability_contracts*.go`) so the engine can validate calls.
4. Write tests using helpers from `vibes/capability_test_helpers_test.go`.
   Aim for both unit tests and a `.vibe` fixture that exercises the adapter
   end-to-end.
5. Add a godoc `Example` so the API surfaces on pkg.go.dev.

## Commit messages

- Imperative present tense subject (`Add foo`, not `Added foo` or `Adds foo`).
- Subject line at most 72 characters.
- Body explains *why* the change is being made and any downstream impact;
  the diff already shows *what*.
- One logical change per commit. Prefer multiple atomic commits to one
  sprawling one.
- Do **not** add a `Co-Authored-By: Claude` trailer.

## Branch naming

Use `<handle>/<short-description>`, for example `jdoe/fix-money-overflow`.
Do not push directly to `master`; open a pull request from a branch.

## Pull requests

- Keep the PR title under 70 characters.
- Body should include a `## Summary` (1–3 bullets) and a `## Test plan`
  checklist of how you verified the change.
- Stack-friendly: if your branch depends on another open PR, base it on that
  branch and call out the dependency in the description.
- CI must be green before merge. The required jobs are:
  - `build-and-test` on Linux, macOS, and Windows (`just test`).
  - `race-detector` (`go test -race ./...`).
  - `quality-gates` (examples coverage, known-issues bug bar, parser +
    runtime fuzz smoke).
  - `just lint` and `vibes fmt -check`.
- After addressing review comments, resolve them on the PR.

## License

Vibescript is MIT licensed; see [`LICENSE`](LICENSE). By contributing you
agree your changes are released under the same license. There is no DCO or
CLA to sign.
