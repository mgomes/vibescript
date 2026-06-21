# Hashes in Vibescript

Hashes are dictionaries whose keys share one string lookup space. Declare common
identifier-shaped keys with Ruby-style shorthand:

```vibe
player = {
  id: "p1",
  name: "Alex",
  raised: money("25.00 USD"),
}
```

Shorthand labels (`name:`) are normalized into the same key space as strings,
so you can access values using either symbol or string notation:
`player[:name]` or `player["name"]`.

Use quoted keys for JSON-shaped payloads or names that are not valid
identifiers:

```vibe
player = {
  "first-name": "Ada",
  "last name": "Lovelace"
}

player["first-name"] # "Ada"
```

Hash rockets are supported for symbol and string keys, and for key expressions
that evaluate to symbols or strings:

```vibe
current_key = :nickname
player = {
  :name => "Ada",
  "first-name" => "Lovelace",
  current_key => "dynamic"
}
```

All hash keys are normalized into the same string lookup space. Other key
types, such as arrays or numbers, raise `unsupported hash key type ...`.

Dot access keeps hash method names reserved. If a stored key is named like a
hash method, use index access for the entry:

```vibe
sizes = { size: "XL" }
sizes.size   # 1
sizes[:size] # "XL"
```

## Query helpers

- `size` / `length`
- `empty?`
- `key?`, `has_key?`, `member?`, `include?` for key membership.
- `value?`, `has_value?` for value membership.

```vibe
def has_required_fields(player)
  player.key?(:name) && player.include?("raised") && !player.empty?
end
```

The key membership predicates accept any candidate key. Hashes only store
symbol and string keys, so a candidate of any other type can never be present
and the predicate simply returns `false` instead of raising.

```vibe
{ a: 1 }.key?(1)     # false
{ a: 1 }.member?(1)  # false
{ a: 1 }.include?(1) # false
```

`value?` and `has_value?` compare the candidate against each stored value using
the same `==` equality as the rest of Vibescript, so deep collections match by
content and integers do not match equal-looking floats.

```vibe
{ a: 1, b: 2 }.value?(2)        # true
{ a: [1, 2] }.has_value?([1, 2]) # true
{ a: 1 }.value?(1.0)            # false
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
- `store(key, value)` returns a new hash with the key assigned, leaving the
  receiver unchanged. Like the other method-based helpers it is immutable-style;
  use index assignment (`hash[key] = value`) when you want to mutate in place.
- `compact` removes `nil` values.
- `slice(*keys)` keeps only selected keys.
- `except(*keys)` removes selected keys.
- `select` / `reject` with a block.
- `transform_keys` / `transform_values` with a block.
- `deep_transform_keys` for recursive key mapping across nested hashes/arrays.
- `remap_keys(mapping_hash)` for direct key rename maps.

```vibe
def public_profile(record)
  record
    .slice(:name, :raised, :goal)
    .reject { |key, value| value == nil }
end
```

```vibe
payload = { player_id: 7, profile: { total_raised: 12 } }
payload.deep_transform_keys do |k|
  if k == :player_id
    :playerId
  elsif k == :total_raised
    :totalRaised
  else
    k
  end
end

{ first_name: "Alex" }.remap_keys({ first_name: :name })
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
