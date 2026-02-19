# VibeScript Overview

VibeScript is a Ruby-inspired DSL for describing fundraising workflows. It
supports literals, collections, hashes, numeric ranges, blocks, and built-in
helpers for money and durations. Many of the examples mirror core Ruby
enumerable patterns, letting non-programmers write expressive scripts that
run inside Go services.

This document covers the basics. See the other files in this folder for deep
dives on specific topics.

## Table of Contents

- `builtins.md` – built-in functions like `assert`, `money`, `now`, and `require`.
- `strings.md` – string manipulation methods like `strip`, `upcase`, and `split`.
- `arrays.md` – working with arrays, including iteration helpers and
  transformations.
- `hashes.md` – symbol-keyed hashes and the merging/lookup helpers we provide.
- `errors.md` – parse and runtime error output, stack traces, and debugging tips.
- `durations.md` – duration literals and time-based helper methods.
- `time.md` – Time creation, formatting, accessors, and time/duration math.
- `typing.md` – gradual typing: annotations, nullables, and type-checked calls.
- `control-flow.md` – conditionals, loops, and ranges.
- `blocks.md` – using block literals for map/select/reduce style patterns.
- `integration.md` – host integration patterns showing how Go services can
  expose capabilities to scripts.
- `module_project_layout.md` – recommended structure for multi-module script
  repositories.
- `module_require_migration.md` – migration checklist for modern `require`
  behavior (exports, aliasing, policy hooks).
- `examples/module_require.md` – practical example showing how to share
  helpers with `require` and module search paths.
- `stdlib_core_utilities.md` – examples for JSON, regex, random IDs, numeric
  conversion, and common time parsing helpers.
- `compatibility.md` – supported Go versions and CI coverage expectations.
