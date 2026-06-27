# Core Syntax Compatibility Guarantees

This document defines the Vibescript core syntax freeze baseline for the `v1.0`
stabilization track.

## Freeze Baseline

The following syntax families are considered core and frozen for compatibility
planning:

- Function declarations (`def ... end`) with positional, default, typed,
  required keyword-only (`name:`), and optional keyword-only (`name: default`)
  parameters.
- Optional return type annotations (`-> type`).
- Class declarations with class/instance methods and variables.
- Core literals: `nil`, booleans, numbers (decimal plus `0x`/`0b`/`0o`/`0d`
  base prefixes), strings, symbols, arrays, hashes, and ranges.
- Assignment to variables, indexes, members, and destructuring targets.
- Control flow: `if`/`elsif`/`else`, `unless`/`else`, `while`, `until`,
  `for ... in`, `break`, `next`, and `return`.
- Block syntax (`do ... end`) and block arguments.
- Structured error handling (`begin`/`rescue`/`ensure`) and `raise`.
- Module loading via `require(...)` with keyword options.

## Compatibility Policy

- Before `v1.0.0`, syntax changes may still occur, but any breaking change must
  be called out in migration notes and release notes.
- After `v1.0.0`, breaking syntax changes require a major version bump.
- Parser behavior for the frozen baseline is protected by
  `internal/runtime/syntax_freeze_test.go`.

For versioning semantics, see `docs/versioning.md`.
