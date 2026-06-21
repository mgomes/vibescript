# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

- Ongoing work toward the next pre-1.0 release.
- **Added: Ruby-style hash member, value, and store helpers.** `Hash#member?`
  joins `key?`/`has_key?`/`include?` as a key-membership alias, `Hash#value?` and
  `Hash#has_value?` report value membership using the same `==` equality as the
  rest of the language, and `Hash#store(key, value)` returns a new hash with the
  key assigned. Like the other method-based hash helpers, `store` is
  immutable-style and leaves the receiver unchanged.
- **Improved: Ruby-style `String#start_with?` and `String#end_with?`.** Both
  predicates now accept one or more string candidates and return true when any
  matches. Candidates are checked left to right and matching short-circuits like
  Ruby, so a non-string candidate is only rejected if reached before a match.
- **Hardened the public jobqueue option parser.** `jobqueue.ParseEnqueueOptions`
  now rejects extra enqueue keywords that are not data-only or that contain
  cyclic references instead of cloning them through to the host, closing a
  contract gap for embedders that call it directly. A new
  `jobqueue.ParseEnqueueOptionsValidated` fast path lets the runtime adapter skip
  the redundant walk when it has already enforced the contract, and the carved
  package gained direct unit tests for constructor validation, retry detection,
  option parsing, cloning, and invalid/cyclic values.
- **Added: `Time#round` precision argument.** `Time#round` now accepts an
  optional Ruby-style `ndigits` (defaulting to `0`) so `round(3)` and `round(6)`
  produce millisecond and microsecond precision, with non-negative `Integer`
  validation and clear errors on misuse.
- **Fixed: Hash membership predicates align with Ruby.** `Hash#key?`,
  `Hash#has_key?`, and `Hash#include?` now return `false` for candidate keys of
  unsupported types instead of raising, matching Ruby's predicate semantics.
- **Added: Ruby-style numeric predicate and successor helpers.** Integers and
  floats gain `zero?`, `positive?`, `negative?`, and `nonzero?` (returning the
  receiver or `nil`), and integers gain `next`/`succ` and `pred`.

## v0.50.0 - 2026-06-11

- **Added: stronger CLI workflows.** `vibes run` now supports inline `-e`
  evaluation and recursive `-watch` mode, and the new `vibes test` command runs
  `.vibe` test files with module-aware fixture coverage.
- **Added: broader LSP support.** The language server now exposes document
  formatting through `vibes fmt`, context-aware completion, signature help,
  go-to-definition, and document symbols, with live-buffer re-anchoring for
  completion and navigation targets.
- **Improved host-facing diagnostics.** Structured parse-error positions are
  available through the public API and LSP, inline snippets remap diagnostics to
  user source positions, lookup failures include did-you-mean suggestions, and
  runtime error wording now follows the documented error-message conventions.
- **Fixed parser and runtime edge cases.** Newline statement boundaries no longer
  accidentally chain line-start expressions, line-ending minus continuations are
  preserved, trailing brace blocks parse correctly, `case` ranges match by
  membership, enum value kinds render distinctly, and hash method names win over
  colliding hash keys in member dispatch.
- **Hardened runtime accounting and arithmetic.** Sandbox limit terminations are
  classified distinctly, capability argument validation runs once while still
  counting validated arguments against memory quota, and integer, duration, and
  time arithmetic now reject `int64` overflow instead of silently wrapping.
- **Improved runtime performance.** Per-call environment and builtin churn,
  memory-estimation work, regex compilation, step context polling, builtin member
  dispatch allocation, and scalar-key array set operations were all tightened
  with regression coverage and benchmark-focused checks.
- **Expanded quality gates and documentation.** CI now enforces coverage, the
  stdlib and LSP documentation were expanded, benchmark artifacts and hotspot
  profiles were refreshed, input-guard limits were centralized, module
  containment edge cases were pinned, and public facade/value package tests were
  added.

## v0.40.0 - 2026-06-06

- **Added: `Tasks` structured concurrency for bounded in-script fanout.**
  Scripts can now use `Tasks.run` to create an automatically awaited task scope,
  `tasks.spawn` to start named function calls, `task.value` to wait for a single
  result, and `Tasks.map` to collect ordered concurrent results.
- **Added host-controlled task concurrency settings.**
  `Config.DefaultTaskConcurrency` defaults task fanout to `4` unless the host
  cap is lower, and `Config.MaxTaskConcurrency` caps script-provided `max:`
  overrides. Requests above the host cap raise an error instead of being
  silently clamped.
- **Hardened task isolation, cancellation, and quota accounting.** Task
  arguments, keyword arguments, results, and inherited mutable globals are cloned
  across task boundaries; task failures propagate through handles or scope exit;
  retained task results count against the parent memory quota while the task
  scope is alive.
- Added a Tasks ADR, README and host-cookbook coverage, a runnable Tasks example,
  `# vibe: 0.4` example headers, deterministic `testing/synctest` coverage for
  concurrency behavior, and a Go 1.26 goroutine leak profile CI gate.

## v0.31.0 - 2026-05-30

- **Fixed: `Money` arithmetic now rejects `int64` overflow instead of silently
  wrapping.** `Add`, `Sub`, `MulInt`, and `DivInt` detect overflow (including
  the `-MinInt64` and `MinInt64 / -1` edges) and return an error, matching the
  range check `ParseMoneyLiteral` already enforces. **Breaking (embedders):
  `value.Money.MulInt` now returns `(Money, error)` instead of `Money`; update
  call sites to handle the error.** Plain integer arithmetic in scripts still
  wraps — money is deliberately stricter.
- **Fixed: deeply nested type annotations can no longer crash the host.** The
  parser bounds type-annotation recursion at depth 64 and emits a normal parse
  error (`type annotation nesting too deep`) instead of overflowing the
  goroutine stack on attacker-supplied source reached through `Engine.Compile`.
- **Fixed: capability contracts now follow builtins captured in closures and
  blocks.** The contract scanner descends into script-function and block
  environments — with a cycle guard and an ambient-global stop — so a contracted
  builtin captured in a closure no longer escapes its `ValidateArgs` /
  `ValidateReturn` enforcement, and an unrelated same-named global is never bound
  to a capability scope through a script-supplied closure. Defense-in-depth: no
  bundled capability returns a closure-wrapped builtin, so default embedders were
  not exposed.

## v0.30.0 - 2026-05-30

- **Fixed: `||` and `&&` now return the surviving operand, not a coerced
  boolean.** `a || b` is `a ? a : b` and `a && b` is `a ? b : a` (Ruby
  semantics), so the documented `value = optional || default` idiom works.
  Previously both collapsed to `true`/`false`. Truthiness rules are unchanged.

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
