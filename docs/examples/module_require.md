# Sharing Helpers with `require`

This example shows how to structure reusable helpers in a module and load them
from another script. The host application whitelists module directories via
`Config.ModulePaths`.

```go
engine := vibes.NewEngine(vibes.Config{
    ModulePaths: []string{"/app/workflows/modules"},
})
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
module’s exports. Functions defined in the module call each other using the
module-local environment, so collisions with host globals do not override the
module’s behaviour.

After the first load the module is cached, making subsequent `require` calls
cheap. To refresh or hot reload modules, restart the embedding application or
clear the engine’s module cache.
