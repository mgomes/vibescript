# Module Project Layout Best Practices

Use a stable directory layout so module paths stay predictable:

```text
workflows/
  modules/
    shared/
      math.vibe
      money.vibe
    billing/
      fees.vibe
      taxes.vibe
  scripts/
    checkout.vibe
    payouts.vibe
```

Guidelines:

- Keep reusable helpers under `modules/shared/`.
- Group domain logic by folder (`billing/`, `risk/`, `reporting/`).
- Prefer `export def ...` for public API surface, keep internals unexported.
- Use `require("module/path", as: "alias")` to avoid global name collisions.
- Use relative requires only within a module subtree (`./`, `../`).
- Configure `ModuleAllowList` and `ModuleDenyList` in hosts that need strict import policy boundaries.

Example:

```vibe
# modules/billing/fees.vibe
export def apply(amount)
  amount + shared_rate()
end

def shared_rate()
  rates = require("../shared/math", as: "math")
  math.double(1)
end
```

```vibe
# scripts/checkout.vibe
def total(amount)
  require("billing/fees", as: "fees")
  fees.apply(amount)
end
```
