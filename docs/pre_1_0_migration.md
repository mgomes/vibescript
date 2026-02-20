# Pre-1.0 Migration Notes

This document tracks known pre-`v1.0.0` breaking changes and how to upgrade.

## Breaking Changes by Version

### v0.4.1: `Time#strftime` renamed to `Time#format`

Before:

```vibe
Time.now.strftime("%Y-%m-%d")
```

After:

```vibe
Time.now.format("2006-01-02")
```

Action: replace `strftime` calls with `format` and use Go time layouts.

### v0.6.0: Constructors switched from panic-style to error-returning APIs

Before:

```go
engine := vibes.MustNewEngine(vibes.Config{})
```

After:

```go
engine, err := vibes.NewEngine(vibes.Config{})
if err != nil {
    return err
}
```

Action: update host bootstrap paths to handle constructor errors explicitly.

### v0.12.0: `require` boundary and module isolation tightened

Changes introduced:

- Path normalization and traversal protections.
- Stricter private helper/module boundary behavior.
- More explicit cycle diagnostics.

Action: ensure module references stay inside configured roots and review module
visibility expectations. See `docs/module_require_migration.md`.

### v0.13.0: Capability contracts enforced at call boundaries

Changes introduced:

- Runtime validation of capability args/kwargs/returns.
- Data-only boundary checks on capability return values.

Action: align adapter payload shapes with declared contracts and add negative
tests for invalid payloads.

### v0.17.0: Module ergonomics updated (exports/aliasing/conflicts)

Changes introduced:

- Explicit export controls supported (`export def`).
- Alias collision and namespace conflict behavior enforced.
- Module allow/deny policy hooks added.

Action: adopt explicit exports and aliases for shared modules, and configure
policy lists in host setup where needed.

## Upgrade Checklist

1. Review release notes for each skipped version.
2. Apply module migration updates (`docs/module_require_migration.md`).
3. Re-run integration tests covering host capabilities and module calls.
4. Verify script behavior under current quotas and strictness settings.
