# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

- Ongoing work toward the next pre-1.0 release.

## v0.21.1 - 2026-03-08

- Fixed bare zero-arg `yield` parsing so newline-separated assignments and expressions no longer get misparsed as implicit yield arguments.
- Added parser regression coverage for zero-arg and inline-arg `yield` forms.
- Added a compile-all `examples/` test to catch unreferenced example parse failures in CI and fixed `examples/blocks/yield_patterns.vibe` to use the intended zero-paren `yield` form again.

## v0.21.0 - 2026-03-08

- Added nominal enums with `::` member access, reflective member helpers, and typed symbol coercion across function and block boundaries.
- Hardened enum and type normalization with case-insensitive resolution, stricter enum-name validation, shadowed-scope lookup fixes, union/hash-key fast-path fixes, and recursive normalization guards.
- Added runnable enum examples and integration coverage, upgraded the REPL to Bubble Tea v2, and added a `just install` recipe for the CLI.
- Strengthened release and quality automation with a race-detector lane, fuzz and benchmark gate tuning, editor support docs, and idempotent release tag reruns.

## v0.20.0 - 2026-02-23

- Runtime call-path performance and benchmark-gating improvements ahead of 1.0.

## v0.1.0 - 2026-02-19

- Initial pre-1.0 project baseline and public documentation set.
