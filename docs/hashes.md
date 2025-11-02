# Hashes in VibeScript

Hashes are symbol-keyed dictionaries. Declare them with Ruby-style shorthand:

```vibe
player = {
  id: "p1",
  name: "Alex",
  raised: money("25.00 USD"),
}
```

Keys default to symbols (`name:`) but you can access values using either symbol
or string notation: `player[:name]` or `player["name"]`.

## Helpers

- `merge` returns a new hash combining receiver and argument.
- `keys` / `values` produce arrays for iteration.
- `deep_fetch_or` (see examples) shows how to supply defaults.

Example:

```vibe
def merge_defaults(player)
  defaults = { goal: 5000, raised: money("0.00 USD") }
  defaults.merge(player)
end
```

Review `examples/hashes/` for live scripts used by the tests.
