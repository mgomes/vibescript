# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

- Confirmed `vibes/value` as the home of `Money`, `Duration`, `Range` rather than carving a separate `vibes/domain`; the Value-payload coupling outweighs the organizational benefit.
- **Breaking (embedders): deprecation aliases from PR-2.x removed.** Update
  imports:
  - `vibes.Value`/`Money`/`Duration`/`Range`/`KindXxx`/`NewXxx` → import
    `github.com/mgomes/vibescript/vibes/value` and qualify (`value.Value`,
    `value.NewInt`, etc.).
  - `vibes.Database`/`DatabaseReader`/`DatabaseWriter`/`DBFindRequest` and
    the other request types → import
    `github.com/mgomes/vibescript/vibes/capability/db`.
  - `vibes.EventPublisher`/`EventPublishRequest` → `events.Publisher`,
    `events.PublishRequest` (import
    `github.com/mgomes/vibescript/vibes/capability/events`).
  - `vibes.JobQueue`/`JobQueueWithRetry`/`JobQueueJob`/
    `JobQueueEnqueueOptions`/`JobQueueRetryRequest` → drop the prefix and
    import `github.com/mgomes/vibescript/vibes/capability/jobqueue`.
  - `vibes.ContextCapabilityResolver` → `contextcap.Resolver` (import
    `github.com/mgomes/vibescript/vibes/capability/contextcap`).
  - AST types under `vibes` (`Program`, `FunctionStmt`, `Identifier`,
    `TypeKind`, `Token*`, etc.) are removed outright; they were already
    `Deprecated:` and pointed at the now-private `internal/ast`. Drive
    scripts through `vibes.Engine` / `vibes.Script` instead.
  The `vibes.NewXxxCapability` / `MustNewXxxCapability` constructors keep
  their names and now consume the subpackage types directly
  (`db.Database`, `events.Publisher`, `jobqueue.JobQueue`,
  `contextcap.Resolver`); call sites just need the new imports.
- Trimmed the public `vibes` package to the curated surface. Engine,
  Script, CallOptions, Execution, Builtin/BuiltinFunc/NewBuiltin/
  NewAutoBuiltin/Builtins, the capability contract types
  (`CapabilityAdapter`, `CapabilityBinding`,
  `CapabilityMethodContract`, `CapabilityContractProvider`),
  RuntimeError/StackFrame, Position, and the capability constructors
  remain. Direct constructors and accessors for blocks, classes,
  instances, enums, and script functions (`NewBlock`, `NewClass`,
  `NewInstance`, `NewEnum`, `NewEnumValue`, `NewFunction`, `BlockOf`,
  `ClassOf`, `InstanceOf`, `FunctionOf`, `EnumOf`, `EnumValueOf`,
  `BuiltinOf`, `ClassDef`, `Instance`, `EnumDef`, `EnumValueDef`,
  `Block`, `ScriptFunction`, `Env`) are no longer exported from `vibes`;
  use the typed payload markers on `value.Value` or work through the
  documented public APIs.
- Migrated `internal/tools/analyze` off the `vibes` facade so it imports
  `internal/runtime` directly for `*runtime.Script` and
  `*runtime.ScriptFunction`. The vibes CLI keeps calling
  `analyze.Script(*vibes.Script)` since `vibes.Script` aliases the
  runtime type.
- Hid the runtime (interpreter, execution engine, module loader, builtins,
  capability adapters) under `internal/runtime`; outside callers can no
  longer import it. `vibes` keeps source-compatible type aliases for
  `Engine`, `Config`, `Script`, `ScriptFunction`, `CallOptions`,
  `Execution`, `Builtin`, `BuiltinFunc`, `Block`, `ClassDef`, `Instance`,
  `EnumDef`, `EnumValueDef`, `Env`, `RuntimeError`, `StackFrame`, and the
  capability interfaces (`CapabilityAdapter`, `CapabilityBinding`,
  `CapabilityMethodContract`, `CapabilityContractProvider`) plus all
  constructor and accessor entry points (`NewEngine`, `NewBuiltin`,
  `NewClass`, `NewInstance`, `NewEnum`, `NewEnumValue`, `NewFunction`,
  `NewBlock`, `BuiltinOf`, `ClassOf`, `InstanceOf`, `FunctionOf`,
  `EnumOf`, `EnumValueOf`, `BlockOf`, `NewDBCapability`,
  `NewEventsCapability`, `NewJobQueueCapability`, `NewContextCapability`,
  etc.). Embedders should see no source-level breakage from this PR.
- Hid the AST and parser under `internal/ast` and `internal/parser`; outside
  callers can no longer import these packages. `vibes` keeps `Deprecated:`
  type aliases for every previously exported AST node so existing embedders
  keep compiling, and the aliases will be removed in v0.29.0. Future AST
  consumers should drive scripts through the `vibes.Engine` / `vibes.Script`
  surface instead.
- Moved `Position` to a new public `github.com/mgomes/vibescript/vibes/source`
  package so the AST (internal) and the public error surface
  (`RuntimeError.Frames[i].Pos`, etc.) can share a single definition without
  forcing AST consumers to import vibes. `vibes.Position` is now a type alias
  for `source.Position`.
- Extracted `cmd/vibes analyze` into `internal/tools/analyze` so the CLI no
  longer touches AST internals directly.
- **Breaking (embedders): `Value` runtime-bound accessors now return marker-interface payloads.**
  `Value.Builtin()`, `Value.Class()`, `Value.Instance()`, `Value.Function()`,
  `Value.Block()`, `Value.Enum()`, and `Value.EnumValue()` now return
  `value.BuiltinPayload` / `value.ClassPayload` / etc. instead of the concrete
  `*vibes.Builtin` / `*vibes.ClassDef` / etc. Migrate by using the typed
  companions `vibes.BuiltinOf(v)`, `vibes.ClassOf(v)`, `vibes.InstanceOf(v)`,
  `vibes.FunctionOf(v)`, `vibes.EnumValueOf(v)`, or by type-asserting against
  the concrete `*vibes.*` types. Data-only accessors (`Bool`, `Int`, `Float`,
  `String`, `Array`, `Hash`, `Money`, `Duration`, `Time`, `Range`) are
  unchanged.
- Carved value-system types into a new `github.com/mgomes/vibescript/vibes/value`
  subpackage. `vibes` re-exports the surface via type aliases and constructor
  wrappers so existing imports keep compiling; the aliases will be removed in
  v0.29.0 alongside direct migration of internal references.
- Carved the database capability into a new
  `github.com/mgomes/vibescript/vibes/capability/db` subpackage. `vibes` keeps
  the public surface intact via `Database`, `DB*Request` type aliases and
  `NewDBCapability` / `MustNewDBCapability` constructor wrappers; the aliases
  will be removed in v0.29.0. Adapters that need the runtime can use the new
  `Execution.Context()` and `Execution.Step()` accessors instead of reaching
  into unexported fields.
- Ongoing work toward the next pre-1.0 release.

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
