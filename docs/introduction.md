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
- `durations.md` – duration literals and time-based helper methods.
- `control-flow.md` – conditionals, loops, and ranges.
- `blocks.md` – using block literals for map/select/reduce style patterns.
- `integration.md` – host integration patterns showing how Go services can
  expose capabilities to scripts.
- `examples/module_require.md` – practical example showing how to share
  helpers with `require` and module search paths.
