# Vibescript Overview

Vibescript is a small, Ruby-inspired language for describing data-driven
workflows inside Go services. It supports named functions, classes, enums,
ordinary control flow, JSON-shaped values, synchronous blocks, and helpers for
money, time, and durations. The familiar syntax is intentionally narrower than
Ruby so hosts can keep execution and authority predictable.

This document covers the basics. See the other files in this folder for deep
dives on specific topics.

## Language Boundary

Script code transforms values and coordinates capabilities supplied by the
embedding host. Arrays and hashes have value semantics, and hashes normalize
string and symbol inputs into one string keyspace. Named functions and methods
are called directly; blocks are syntax attached to the call that runs them and
cannot escape as stored callbacks. Modules are explicit namespaces rather than
mixins.

The Go host owns concurrency, delay, scheduling, and external effects. It can
run independent `Script.Call` invocations concurrently or expose a bounded
batch, job, database, or event capability when a workflow needs those services.
See the [language reference](language_reference.md),
[1.0 migration guide](migrating-to-1.0.md), and
[ADR-006](adr/006-slim-language-for-predictable-sandboxing.md) for the complete
contract.

## Table of Contents

- `builtins.md` – built-in functions like `assert`, `money`, `now`, and `require`.
- `strings.md` – string manipulation methods like `strip`, `upcase`, and `split`.
- `arrays.md` – working with arrays, including iteration helpers and
  transformations.
- `hashes.md` – hashes with one string keyspace and the merging/lookup helpers we provide.
- `errors.md` – parse and runtime error output, stack traces, and debugging tips.
- `durations.md` – duration literals and time-based helper methods.
- `time.md` – Time creation, formatting, accessors, and time/duration math.
- `typing.md` – gradual typing: annotations, nullables, and type-checked calls.
- `enums.md` – nominal enums, `::` member access, and typed symbol coercion.
- `classes.md` – class syntax, class/instance methods, variables, and privacy.
- `language_reference.md` – consolidated language syntax and semantics reference.
- `syntax_compatibility.md` – core syntax freeze baseline and compatibility guarantees.
- `migrating-to-1.0.md` – breaking changes in the 1.0 release with
  before/after examples and fixes.
- `control-flow.md` – conditionals, loops, and ranges.
- `blocks.md` – using block literals for map/select/reduce style patterns.
- `tooling.md` – CLI workflows for running, checking, formatting, analyzing,
  testing, editor integration, and the REPL.
- `architecture.md` – internal runtime/parser/module architecture map for maintainers.
- `integration.md` – host integration patterns showing how Go services can
  expose capabilities to scripts.
- `host_cookbook.md` – production embedding patterns and operational guidance.
- `starter_templates.md` – starter scaffolds for common embedding scenarios.
- `module_project_layout.md` – recommended structure for multi-module script
  repositories.
- `module_require_migration.md` – migration checklist for modern `require`
  behavior (exports, aliasing, policy hooks).
- `examples/module_require.md` – practical example showing how to share
  helpers with `require` and module search paths.
- `stdlib_core_utilities.md` – complete method reference for strings, arrays,
  hashes, numerics, money, durations, times, and builtin functions.
- `compatibility.md` – supported Go versions and CI coverage expectations.
- `versioning.md` – semantic versioning policy and compatibility contract.
- `deprecation_policy.md` – lifecycle policy for public Go embedding APIs.
- `known_issues.md` – active P0/P1 correctness bug bar.
