# VibeScript Roadmap and Implementation Checklist

This is the working implementation TODO for upcoming VibeScript releases.

How to use this file:

- Keep release scope realistic and bias toward shipping.
- Move unfinished items forward instead of silently dropping them.
- Link each checklist item to an issue/PR once created.
- Mark items complete only after tests/docs are updated.

Release status legend:

- `[ ]` not started
- `[~]` in progress (replace manually while working)
- `[x]` complete

---

## Historical Completed Releases

These releases are already shipped and tagged.
Completion dates reflect the corresponding git tag date.

### v0.1.0 (completed 2025-12-15)

- [x] Added GitHub Actions CI workflow.
- [x] Added stack traces to runtime errors.
- [x] Expanded baseline stdlib coverage.
- [x] Added optional-parens syntax support.
- [x] Added initial CLI support.
- [x] Fixed package/versioning setup for release tagging.

### v0.2.0 (completed 2025-12-15)

- [x] Added GoReleaser-based release automation.
- [x] Added recursion depth limit enforcement.
- [x] Added recursion limit test coverage.

### v0.2.1 (completed 2025-12-15)

- [x] Pinned GoReleaser version for stable release builds.

### v0.2.2 (completed 2025-12-15)

- [x] Adjusted GoReleaser configuration.

### v0.3.0 (completed 2025-12-20)

- [x] Added `Number#times`.
- [x] Added `Duration` class modeled after ActiveSupport-style duration semantics.

### v0.4.0 (completed 2025-12-21)

- [x] Added duration arithmetic support.
- [x] Improved optional-parens behavior for zero-arg methods.
- [x] Implemented `Time` class.

### v0.4.1 (completed 2025-12-21)

- [x] Renamed `Time#strftime` to `Time#format` for Go layout alignment.
- [x] Added Duration and Time documentation.

### v0.5.0 (completed 2025-12-26)

- [x] Added first pass of gradual typing.
- [x] Added support for classes.
- [x] Added block arguments in method definitions.
- [x] Added numeric literal underscore separators.
- [x] Expanded complex tests/examples coverage.

### v0.5.1 (completed 2026-02-07)

- [x] Expanded test suite breadth.
- [x] Landed performance optimizations and refactors.
- [x] Exported `CallBlock()` for embedders.

### v0.6.0 (completed 2026-02-11)

- [x] Added runtime memory quota enforcement.
- [x] Enforced strict effects for globals and `require`.
- [x] Isolated `Script.Call` runtime state per invocation.
- [x] Replaced panicking constructors with error-returning APIs.
- [x] Increased CLI/REPL test coverage and added execution benchmarks.

### v0.7.0 (completed 2026-02-12)

- [x] Shipped multi-phase string helper expansion.
- [x] Added regex/byte helper coverage and bang-method parity improvements.

### v0.8.0 (completed 2026-02-12)

- [x] Expanded Hash built-ins and hash manipulation support.
- [x] Refreshed hash docs and examples.

### v0.9.0 (completed 2026-02-12)

- [x] Expanded Array helper surface and enumerable workflows.

### v0.10.0 (completed 2026-02-12)

- [x] Made Time and numeric APIs coherent with documented behavior.

### v0.11.0 (completed 2026-02-12)

- [x] Improved parse/runtime error feedback and debugging quality.

### v0.12.0 (completed 2026-02-13)

- [x] Hardened `require` behavior for safer module composition.
- [x] Improved private helper/module boundary behavior.
- [x] Improved circular dependency diagnostics for modules.

### v0.13.0 (completed 2026-02-17)

- [x] Enforced capability contracts at runtime boundaries.
- [x] Added contract validation paths for capability args and returns.
- [x] Improved capability isolation and contract binding behavior.

---

## v0.14.0 - Capability Foundations (completed 2026-02-18)

Goal: make host integrations first-class and safe enough for production workflows.

### Capability Adapters

- [x] Add first-party `db` capability adapter interface and implementation.
- [x] Add first-party `events` capability adapter interface and implementation.
- [x] Add first-party `ctx` capability adapter for request/user/tenant metadata.
- [x] Define naming conventions for adapter method exposure (`cap.method`).
- [x] Ensure all adapters support context propagation and cancellation.

### Contracts and Safety

- [x] Add capability method contracts for all new adapter methods.
- [x] Validate args/kwargs/returns for `db` adapter methods.
- [x] Validate args/kwargs/returns for `events` adapter methods.
- [x] Add explicit data-only boundary checks for all capability returns.
- [x] Add contract error messages with actionable call-site context.

### Script Surface

- [x] Promote `examples/background/` scenarios to fully supported behavior.
- [x] Convert `examples/future/iteration.vibe` from stretch to supported.
- [x] Add docs for common capability patterns (query + transform + enqueue).
- [x] Add docs for capability failure handling patterns.

### Testing and Hardening

- [x] Add unit tests for each capability adapter method.
- [x] Add integration tests for mixed capability calls in one script.
- [x] Add negative tests for type violations and invalid payload shapes.
- [x] Add quota/recursion interaction tests with capability-heavy scripts.
- [x] Add benchmarks for capability call overhead.

### v0.14.0 Definition of Done

- [x] All new capabilities documented in `docs/integration.md`.
- [x] Background/future examples run in CI examples suite.
- [x] No contract bypasses in capability call boundaries.

---

## v0.15.0 - Type System v2

Goal: make types expressive enough for real workflows while keeping runtime checks predictable.

### Type Features

- [x] Add parametric container types: `array<T>`, `hash<K, V>`.
- [x] Add union types beyond nil: `A | B`.
- [x] Add typed object/hash shape syntax for common payload contracts.
- [x] Add typed block signatures where appropriate.
- [x] Define type display formatting for readable runtime errors.

### Type Semantics

- [x] Specify variance/invariance rules for container assignments.
- [x] Specify nullability interactions with unions (`T?` vs `T | nil`).
- [x] Define coercion policy (no coercion vs explicit coercion helpers).
- [x] Decide strictness for unknown keyword args under typed signatures.

### Runtime and Parser

- [x] Extend parser grammar for generic and union type expressions.
- [x] Extend type resolver and internal type representation.
- [x] Add runtime validators for composite/union types.
- [x] Add contract interop so capability contracts can reuse type validators.

### Testing and Docs

- [x] Add parser tests for all new type syntax forms.
- [x] Add runtime tests for nested composite type checks.
- [x] Add regression tests for existing `any` and nullable behavior.
- [x] Expand `docs/typing.md` with migration examples.

### v0.15.0 Definition of Done

- [x] Existing scripts without annotations remain compatible.
- [x] Type errors include parameter name, expected type, and actual type.
- [x] Capability contract validation can use the same type primitives.

---

## v0.16.0 - Control Flow and Error Handling

Goal: improve language ergonomics for complex script logic and recovery behavior.

### Control Flow

- [x] Add `while` loops.
- [x] Add `until` loops.
- [x] Add loop control keywords: `break` and `next`.
- [x] Add `case/when` expression support (if approved).
- [x] Define behavior for nested loop control and block boundaries.

### Error Handling Constructs

- [x] Add structured error handling syntax (`begin/rescue/ensure` or equivalent).
- [x] Add typed error matching where feasible.
- [x] Define re-raise semantics and stack preservation.
- [x] Ensure runtime errors preserve original position and call frames.

### Runtime Behavior

- [x] Ensure new control flow integrates with step quota accounting.
- [x] Ensure new constructs integrate with recursion/memory quotas.
- [x] Validate behavior inside class methods, blocks, and capability callbacks.

### Testing and Docs

- [x] Add parser/runtime tests for each new control flow construct.
- [x] Add nested control flow tests for edge cases.
- [x] Add docs updates in `docs/control-flow.md` and `docs/errors.md`.
- [x] Add examples under `examples/control_flow/` for each new feature.

### v0.16.0 Definition of Done

- [x] No regressions in existing `if/for/range` behavior.
- [x] Structured error handling works with assertions and runtime errors.
- [x] Coverage includes nested/edge control-flow paths.

---

## v0.17.0 - Modules and Package Ergonomics

Goal: make multi-file script projects easier to compose and maintain.

### Module System

- [x] Add explicit export controls (beyond underscore naming).
- [x] Add import aliasing for module objects.
- [x] Define and enforce module namespace conflict behavior.
- [x] Improve cycle error diagnostics with concise chain rendering.
- [x] Add module cache invalidation policy for long-running hosts.

### Security and Isolation

- [x] Tighten module root boundary checks and path normalization.
- [x] Add test coverage for path traversal attempts.
- [x] Add explicit policy hooks for module allow/deny lists.

### Developer UX

- [x] Add docs for module project layout best practices.
- [x] Add examples for reusable helper modules and namespaced imports.
- [x] Add migration guide for existing `require` users.

### v0.17.0 Definition of Done

- [x] Module APIs are explicit and predictable.
- [x] Error output for cycle/import failures is actionable.
- [x] Security invariants around module paths are fully tested.

---

## v0.18.0 - Standard Library Expansion

Goal: reduce host-side boilerplate for common scripting tasks.

### Core Utilities

- [x] Add JSON parse/stringify built-ins.
- [x] Add regex matching/replacement helpers.
- [x] Add UUID/random identifier utilities with deterministic test hooks.
- [x] Add richer date/time parsing helpers for common layouts.
- [x] Add safer numeric conversions and clamp/round helpers.

### Collections and Strings

- [x] Expand hash helpers for nested transforms and key remapping.
- [x] Expand array helpers for chunking/windowing and stable group operations.
- [x] Add string helpers for common normalization and templating tasks.

### Compatibility and Safety

- [x] Define deterministic behavior for locale-sensitive operations.
- [x] Add quotas/guards around potentially expensive operations.
- [x] Ensure new stdlib functions are capability-safe where required.

### Testing and Docs

- [x] Add comprehensive docs pages and examples for each new family.
- [x] Add negative tests for malformed JSON/regex patterns.
- [x] Add benchmark coverage for hot stdlib paths.

### v0.18.0 Definition of Done

- [x] New stdlib is documented and example-backed.
- [x] Runtime behavior is deterministic across supported OSes.
- [x] Security/performance guardrails are validated by tests.

---

## v0.19.0 - Tooling, Quality, and Performance

Goal: improve day-to-day developer productivity and interpreter robustness.

### Tooling

- [x] Add canonical formatter command and CI check.
- [x] Add language server protocol (LSP) prototype (hover, completion, diagnostics).
- [x] Add static analysis command for script-level linting.
- [x] Improve REPL inspection commands (globals/functions/types).

### Runtime Quality

- [x] Profile evaluator hotspots and optimize dispatch paths.
- [x] Reduce allocations in common value transformations.
- [x] Improve error rendering for deeply nested call stacks.
- [x] Add fuzz tests for parser and runtime edge cases.

### CI and Release Engineering

- [x] Add smoke tests for docs examples to CI.
- [x] Add release checklist automation for changelog/version bumps.
- [x] Add compatibility matrix notes for supported Go versions.

### v0.19.0 Definition of Done

- [x] Tooling commands are documented and stable.
- [x] Performance regressions are tracked with benchmarks.
- [x] CI includes example and fuzz coverage gates.

---

## v1.0.0 - Stabilization and Public API Commitment

Goal: lock the language and embedding API for long-term support.

### Stabilization

- [x] Freeze core syntax and document compatibility guarantees.
- [x] Freeze public Go embedding APIs or publish deprecation policy.
- [x] Publish semantic versioning and compatibility contract.
- [x] Complete migration notes for all pre-1.0 breaking changes.

### Documentation and Adoption

- [x] Publish complete language reference.
- [x] Publish host integration cookbook with production patterns.
- [x] Provide starter templates for common embedding scenarios.

### Final Readiness

- [x] Zero known P0/P1 correctness bugs.
- [x] CI green across supported platforms and Go versions.
- [x] Release process rehearsed and repeatable.

---

## v0.20.0 - Performance and Benchmarking (1.0 Push)

Goal: make performance improvements measurable, repeatable, and protected against regressions.

### Runtime Performance

- [ ] Profile evaluator hotspots and prioritize top 3 CPU paths by cumulative time.
- [ ] Reduce `Script.Call` overhead for short-running scripts (frame/env setup and teardown).
- [ ] Optimize method dispatch and member access fast paths.
- [ ] Reduce allocations in common collection transforms (`map`, `select`, `reduce`, `chunk`, `window`).
- [ ] Optimize typed argument/return validation for nested composite types.

### Memory and Allocation Discipline

- [ ] Reduce transient allocations in stdlib JSON/Regex/String helper paths.
- [ ] Reduce temporary map/array churn in module and capability boundary code paths.
- [ ] Add per-benchmark allocation targets (`allocs/op`) for hot runtime paths.
- [ ] Add focused regression tests for high-allocation call patterns.

### Benchmark Coverage

- [ ] Expand benchmark suite for compile, call, control-flow, and typed-runtime workloads.
- [ ] Add capability-heavy benchmarks (db/events/context adapters + contract validation).
- [ ] Add module-system benchmarks (`require`, cache hits, cache misses, cycle paths).
- [ ] Add stdlib benchmarks for JSON/Regex/Time/String/Array/Hash hot operations.
- [ ] Add representative end-to-end benchmarks using `tests/complex/*.vibe` workloads.

### Benchmark Tooling and CI

- [x] Add a single benchmark runner command/script with stable flags and output format.
- [ ] Persist benchmark baselines in versioned artifacts for release comparison.
- [x] Add PR-time benchmark smoke checks with threshold-based alerts.
- [ ] Add scheduled full benchmark runs with trend reporting.
- [ ] Document benchmark interpretation and triage workflow.

### Profiling and Diagnostics

- [ ] Add reproducible CPU profile capture workflow for compile and runtime benchmarks.
- [ ] Add memory profile capture workflow for allocation-heavy scenarios.
- [ ] Add flamegraph generation instructions and hotspot triage checklist.
- [ ] Add a short "performance playbook" for validating optimizations before merge.

### v0.20.0 Definition of Done

- [ ] Benchmarks cover runtime, capability, module, and stdlib hot paths.
- [ ] CI reports benchmark deltas for guarded smoke benchmarks.
- [ ] Measurable improvements are achieved before the v1.0.0 release tag.
- [ ] Performance and benchmarking workflows are documented and maintainable.
