# Public API Deprecation Policy

This document defines how VibeScript evolves the public Go embedding API in
the `vibes` module.

## Scope

The policy applies to exported APIs intended for embedders, including:

- Engine/script lifecycle APIs (`NewEngine`, `Engine.Compile`, `Script.Call`).
- Configuration and execution contracts (`Config`, `CallOptions`).
- Capability adapter interfaces and related public value types.

## Deprecation Process

When an API needs to change:

1. Add a `Deprecated:` notice to the Go doc comment.
2. Document the replacement path in release notes and migration docs.
3. Keep the deprecated API available for the policy window unless a critical
   security/correctness issue requires immediate removal.

## Support Windows

Before `v1.0.0`:

- Deprecated embedding APIs remain available for at least one minor release.
- Breaking API removals happen only in minor releases.

After `v1.0.0`:

- Deprecated embedding APIs remain available for at least two minor releases.
- Removals require a major version bump.

## Emergency Exceptions

Security and correctness fixes may shorten the window. When this happens, the
release notes must include:

- The reason for the expedited change.
- A concrete migration path.
- Affected versions and hosts.
