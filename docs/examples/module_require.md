# Sharing Helpers with `require`

This example shows how to structure reusable helpers in a module and load them
from another script. The host application whitelists module directories via
`Config.ModulePaths`.

```go
engine, err := vibes.NewEngine(vibes.Config{
    ModulePaths: []string{"/app/workflows/modules"},
})
if err != nil {
    panic(err)
}
```

Place `modules/fees.vibe` on disk:

```vibe
# modules/fees.vibe

export def apply_fee(amount)
  amount + rate
end

def rate
  1
end

def _rounding_hint
  "bankers"
end
```

A main script can load the helpers and use the exported functions:

```vibe
# scripts/checkout.vibe

def total_with_fee(amount)
  require("fees", as: "fees")
  fees.apply_fee(amount)
end
```

Namespaced imports scale better as helper sets grow:

```vibe
def quote_total(amount)
  require("billing/fees", as: "fees")
  require("billing/taxes", as: "taxes")
  taxes.apply(fees.apply(amount))
end
```

When `total_with_fee` runs, `require("fees")` resolves the module relative to
`Config.ModulePaths`, compiles it once, and returns an object containing the
module’s exports. Use `export def` for explicit control; if no explicit exports
are declared, public functions are exported by default and names starting with
`_` stay private to the module. When an exported name conflicts with an
existing global, the existing binding keeps precedence and the module object
remains the conflict-free access path.

Inside modules, explicit relative requires are supported:

```vibe
# modules/pricing/tax.vibe
def compute(amount)
  rates = require("./rates")
  amount * rates.current
end
```

Reusable helper modules can be shared from a central namespace:

```vibe
# modules/shared/currency.vibe
export def cents(value)
  value * 100
end

# modules/billing/taxes.vibe
export def apply(amount)
  require("../shared/currency", as: "currency")
  amount + currency.cents(1)
end
```

Relative requires (`./` and `../`) resolve from the requiring module’s
directory and cannot escape the configured module root.

After the first load the module is cached, making subsequent `require` calls
cheap. To refresh or hot reload modules, restart the embedding application or
call `engine.ClearModuleCache()` between runs.
