# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

- Ongoing work toward the next pre-1.0 release.

## v0.26.3 - 2026-03-08

- Followed up the Rosetta compatibility patch by preserving explicit multiline continuations in line-limited control-flow headers and statement expressions.
- Matched Ruby-style floor semantics for signed `int / int` and `int % int`, so negative-input algorithms stay in the expected integer space.
- Hardened `array.fetch` to reject fractional numeric indices instead of truncating them silently.
- Added regression coverage for multiline chained header conditions, signed integer arithmetic, and stricter `array.fetch` index validation.

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
