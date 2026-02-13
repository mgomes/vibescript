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

## Query helpers

- `size` / `length`
- `empty?`
- `key?`, `has_key?`, `include?`

```vibe
def has_required_fields(player)
  player.key?(:name) && player.include?("raised") && !player.empty?
end
```

## Access helpers

- `fetch(key, default=nil)` to supply defaults for missing keys.
- `dig(*path)` for nested lookup.

```vibe
def display_name_or_default(records, player_id)
  fallback = "unknown"
  records.dig(player_id, :meta, :display_name) || fallback
end
```

## Transform and filter helpers

- `merge` combines two hashes into a new hash.
- `compact` removes `nil` values.
- `slice(*keys)` keeps only selected keys.
- `except(*keys)` removes selected keys.
- `select` / `reject` with a block.
- `transform_keys` / `transform_values` with a block.

```vibe
def public_profile(record)
  record
    .slice(:name, :raised, :goal)
    .reject { |key, value| value == nil }
end
```

## Iteration helpers

- `keys` and `values`
- `each`, `each_key`, `each_value`

`keys`, `values`, and block-based hash iteration process entries in sorted key
order for deterministic behavior.

Example:

```vibe
def merge_defaults(player)
  defaults = { goal: 5000, raised: money("0.00 USD") }
  defaults.merge(player)
end
```

Review `examples/hashes/` for live scripts used by the tests.
