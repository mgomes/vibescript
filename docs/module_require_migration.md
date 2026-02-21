# Migration Guide for `require`

This guide helps older scripts move to the current module model.

## 1. Use `private def` for module-only helpers

Before:

```vibe
def apply_fee(amount)
  amount + 1
end
```

After:

```vibe
private def helper_fee(amount)
  amount + 1
end

def apply_fee(amount)
  helper_fee(amount)
end
```

Functions are exported by default. Mark non-public helpers with `private def`.

## 2. Use aliases for namespacing

Before:

```vibe
fees = require("fees")
fees.apply_fee(amount)
```

After:

```vibe
require("fees", as: "fees")
fees.apply_fee(amount)
```

Aliases make import intent explicit and reduce global collisions.

## 3. Plan for conflict behavior

- Existing globals are not overwritten by module exports.
- Access conflicting functions through the returned/aliased module object.
- Alias collisions raise runtime errors (`alias "<name>" already defined`).

## 4. Add policy boundaries in hosts

For long-running or multi-tenant hosts:

- Configure `ModuleAllowList` and `ModuleDenyList`.
- Use `engine.ClearModuleCache()` when module sources may change.

## 5. Validate with tests

- Add integration tests for required module paths.
- Add negative tests for denied modules and traversal attempts.
- Verify cycle errors are actionable (`a -> b -> a`).
