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

def rate()
  1
end

def apply_fee(amount)
  amount + rate()
end

def _rounding_hint()
  "bankers"
end
```

A main script can load the helpers and use the exported functions:

```vibe
# scripts/checkout.vibe

def total_with_fee(amount)
  fees = require("fees")
  fees.apply_fee(amount)
end
```

When `total_with_fee` runs, `require("fees")` resolves the module relative to
`Config.ModulePaths`, compiles it once, and returns an object containing the
module’s public exports. Function names starting with `_` stay private to the
module and are not exposed on the returned object or injected globally.

Inside modules, explicit relative requires are supported:

```vibe
# modules/pricing/tax.vibe
def compute(amount)
  rates = require("./rates")
  amount * rates.current()
end
```

Relative requires (`./` and `../`) resolve from the requiring module’s
directory and cannot escape the configured module root.

After the first load the module is cached, making subsequent `require` calls
cheap. To refresh or hot reload modules, restart the embedding application or
clear the engine’s module cache.
