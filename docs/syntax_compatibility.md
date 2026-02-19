# Core Syntax Compatibility Guarantees

This document defines the VibeScript core syntax freeze baseline for the `v1.0`
stabilization track.

## Freeze Baseline

The following syntax families are considered core and frozen for compatibility
planning:

- Function declarations (`def ... end`) with positional, keyword/default, and
  typed parameters.
- Optional return type annotations (`-> type`).
- Class declarations with class/instance methods and variables.
- Core literals: `nil`, booleans, numbers, strings, symbols, arrays, hashes,
  and ranges.
- Control flow: `if`/`elsif`/`else`, `while`, `until`, `for ... in`,
  `break`, `next`, and `return`.
- Block syntax (`do ... end`) and block arguments.
- Structured error handling (`begin`/`rescue`/`ensure`) and `raise`.
- Module loading via `require(...)` with keyword options.

## Compatibility Policy

- Before `v1.0.0`, syntax changes may still occur, but any breaking change must
  be called out in migration notes and release notes.
- After `v1.0.0`, breaking syntax changes require a major version bump.
- Parser behavior for the frozen baseline is protected by
  `vibes/syntax_freeze_test.go`.

For versioning semantics, see `docs/versioning.md`.
