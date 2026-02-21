# Versioning and Compatibility Contract

VibeScript uses [Semantic Versioning](https://semver.org/) for tags (`vMAJOR.MINOR.PATCH`).

## Meaning of Version Components

- `MAJOR`: incompatible language/runtime/embedding API changes.
- `MINOR`: backward-compatible features and additive API changes.
- `PATCH`: backward-compatible bug fixes, security fixes, and docs/test-only adjustments.

## Pre-1.0 Policy

Before `v1.0.0`, `MINOR` releases may include breaking changes while language and host APIs are still being finalized.

Pre-1.0 rules:

- Breaking changes go in `MINOR` releases.
- `PATCH` releases stay backward-compatible.
- Each breaking change must include migration notes in release notes or docs.

## Post-1.0 Policy

After `v1.0.0`:

- Language syntax/behavior and public embedding APIs follow strict semver.
- Breaking changes require a `MAJOR` bump.
- Deprecations are announced before removal and tracked in release notes.

## Compatibility Scope

This contract applies to:

- VibeScript syntax and runtime behavior.
- Public Go embedding APIs in the `vibes` module.
- CLI command surface (`vibes run`, `vibes fmt`, `vibes analyze`, `vibes lsp`, `vibes repl`).

Go toolchain support follows `docs/compatibility.md`.
Core language syntax guarantees are tracked in `docs/syntax_compatibility.md`.
Pre-1.0 migration guidance is communicated in release notes and pull request descriptions.
