# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

- Ongoing work toward the next pre-1.0 release.

## v0.29.0 - 2026-05-17

- Completed the embedder-facing package refactor: value-system types now live
  in `github.com/mgomes/vibescript/vibes/value`, capability adapter contracts
  live under `vibes/capability/{contextcap,db,events,jobqueue}`, and source
  positions live in `vibes/source`.
- Hid the AST, parser, runtime engine, module loader, builtins, and runtime
  capability adapters under `internal/` packages so the public API is centered
  on `vibes.Engine`, `vibes.Script`, capability construction, runtime errors,
  and value/capability subpackages.
- **Breaking (embedders): removed the v0.28 deprecation alias bridge.**
  Update imports from root `vibes` to the new subpackages:
  - `vibes.Value`, `Money`, `Duration`, `Range`, `KindXxx`, and `NewXxx` move
    to `github.com/mgomes/vibescript/vibes/value`.
  - `vibes.Database`, `DatabaseReader`, `DatabaseWriter`, `DBFindRequest`, and
    related database request/result types move to
    `github.com/mgomes/vibescript/vibes/capability/db`.
  - `vibes.EventPublisher` and `EventPublishRequest` move to
    `github.com/mgomes/vibescript/vibes/capability/events` as
    `events.Publisher` and `events.PublishRequest`.
  - `vibes.JobQueue`, `vibes.JobQueueWithRetry`, `vibes.JobQueueJob`,
    `vibes.JobQueueEnqueueOptions`, and `vibes.JobQueueRetryRequest` move to
    `github.com/mgomes/vibescript/vibes/capability/jobqueue` as
    `jobqueue.JobQueue`, `jobqueue.JobQueueWithRetry`,
    `jobqueue.JobQueueJob`, `jobqueue.JobQueueEnqueueOptions`, and
    `jobqueue.JobQueueRetryRequest`.
  - `vibes.ContextCapabilityResolver` moves to
    `github.com/mgomes/vibescript/vibes/capability/contextcap` as
    `contextcap.Resolver`.
  - Previously deprecated AST/parser types under `vibes` are removed; drive
    scripts through `vibes.Engine` and `vibes.Script` instead.
- **Breaking (embedders): root `vibes` no longer exports direct runtime payload
  constructors or accessors** for blocks, classes, instances, enums, or script
  functions. Use the documented engine/script APIs and typed payload markers on
  `value.Value`.
- Updated `Value` runtime-bound accessors so builtin, class, instance, function,
  block, enum, and enum-value accessors return `value.*Payload` marker
  interfaces instead of concrete runtime types. Data-only accessors such as
  `Bool`, `Int`, `Float`, `String`, `Array`, `Hash`, `Money`, `Duration`,
  `Time`, and `Range` are unchanged.
- Extracted `cmd/vibes analyze` into `internal/tools/analyze`, added CLI package
  documentation, added public API Godoc examples, and documented `vibes/value`
  as the home for `Money`, `Duration`, and `Range`.
- Added a `golangci-lint` baseline, opt-in pre-commit hook, contribution guide,
  and stronger lint/benchmark automation.
- Modernized the test suite with focused internal runtime/parser/AST packages,
  table-driven cases, `cmp.Diff` snapshots, shared CLI helpers, Godoc examples,
  and broader safe `t.Parallel` coverage.

## v0.28.2 - 2026-05-16

- Fixed a quadratic `combineErrors` path where invalid-UTF-8 input drove CPU usage that scaled with the square of the number of parse errors, closing a cheap server-side DoS vector.
- Closed a module-policy bypass where require arguments that normalized to empty (e.g. `.vibe`, `..vibe`, `0.vibe.vibe`) silently skipped allow/deny enforcement.
- Aligned module-policy normalization with the loader by stripping at most one implicit `.vibe` so allow-lists no longer widen to sibling files like `helper.vibe.vibe` or `pkg/..vibe`.
- Added the fuzz-minimized regression inputs to the committed corpus and expanded the policy invariant tests so the bypass classes are pinned for future runs.

## v0.28.1 - 2026-05-15

- Fixed module policy normalization so whitespace-only path segments cannot produce non-idempotent policy patterns or module names.
- Added the fuzz-minimized module policy case to the committed corpus so the regression is replayed by normal tests and future fuzz runs.

## v0.28.0 - 2026-05-15

- Added broad fuzz coverage across command input paths, formatting, lexing, parsing, compilation, runtime execution, JSON/value conversion, module handling, capability validation, and scalar input helpers.
- Added a `just fuzz` sweep with a 10-second default and a nightly GitHub Actions fuzz workflow for heavier automated coverage.
- Raised the LSP JSON-RPC payload cap so valid near-1 MiB source files are not rejected solely because of protocol framing overhead.
- Restored dot access for keyword-named hash/object members loaded from JSON or remapped data, such as `payload.raise` and `payload.begin`.

## v0.27.0 - 2026-05-04

- Hardened engine API snapshot boundaries so caller-mutated snapshots cannot corrupt later executions, including deep-cloned object-valued builtin tables.
- Tightened module containment by freezing configured module roots at engine creation, preventing cwd/symlink drift, and rejecting non-regular module files before reading.
- Aligned regex-based string helpers with the guarded `Regex` builtins for pattern, input, replacement, and output size limits.
- Added containment coverage for cyclic host arrays, mutable API snapshots, module root drift, regex guard bypasses, and related breakout paths while preserving benchmark smoke gates.
- Cleaned up Go API boundary and test hygiene with stronger error matching, interface checks, documentation, and focused performance follow-ups.

## v0.26.2 - 2026-03-08

- Fixed newline-sensitive parsing in control-flow headers and statement expressions so next-line literals no longer get consumed accidentally while explicit multiline continuations still work.
- Made `&&` and `||` short-circuit lazily and aligned integer division/modulo with Ruby-style floor semantics for signed integer algorithm ports.
- Added Ruby-friendly array query aliases and helpers with `length`, `empty?`, and `fetch`, plus stricter `array.fetch` index validation.
- Expanded regression coverage for Rosetta-style examples with multiline header parsing, short-circuit guards, signed integer arithmetic, and array helper behavior.

## v0.21.0 - 2026-03-08

- Added nominal enums with `::` member access, reflective member helpers, and typed symbol coercion across function and block boundaries.
- Hardened enum and type normalization with case-insensitive resolution, stricter enum-name validation, shadowed-scope lookup fixes, union/hash-key fast-path fixes, and recursive normalization guards.
- Added runnable enum examples and integration coverage, upgraded the REPL to Bubble Tea v2, and added a `just install` recipe for the CLI.
- Strengthened release and quality automation with a race-detector lane, fuzz and benchmark gate tuning, editor support docs, and idempotent release tag reruns.

## v0.20.0 - 2026-02-23

- Runtime call-path performance and benchmark-gating improvements ahead of 1.0.

## v0.1.0 - 2026-02-19

- Initial pre-1.0 project baseline and public documentation set.
