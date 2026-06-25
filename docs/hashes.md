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

Reserved words are valid shorthand labels when followed by an explicit value, so
keyword-shaped payload keys behave like any other label:

```vibe
result = { begin: 0, rescue: "retry", ensure: true, raise: false, return: 1 }
result[:rescue] # "retry"
```

This mirrors Ruby, which treats keyword-shaped labels uniformly. The same labels
are accepted as keyword arguments at call sites, with or without parentheses
(`record(rescue: "retry")` and `record rescue: "retry"`). The trailing colon
disambiguates the label from the keyword, so `record rescue: "retry"` passes a
keyword argument rather than parsing `rescue` as a control-flow keyword.

Hash literals only accept colon-style keys: shorthand labels (`name:`) and
quoted string keys (`"name":`). Ruby's hash rocket syntax (`=>`) is not
supported, so write `{ name: "Ada" }` rather than `{ :name => "Ada" }`. To use a
value computed at runtime as a key, assign into the hash after constructing it:

```vibe
current_key = :nickname
player = { name: "Ada" }
player[current_key] = "dynamic"
```

Keys assigned through index access are normalized into the same string lookup
space. Other key types, such as arrays or numbers, raise
`unsupported hash key type ...`.

Dot access keeps hash method names reserved. If a stored key is named like a
hash method, use index access for the entry:

```vibe
sizes = { size: "XL" }
sizes.size   # 1
sizes[:size] # "XL"
```

## Default values

Hash literals return `nil` for a missing key. To return something else, build the
hash with `Hash.new` and a Ruby-style default. `Hash.new` accepts either a
default value or a default proc, but not both:

```vibe
counts = Hash.new(0)
counts[:misses]   # 0   (the default value)
counts.size       # 0   (a value default never inserts)

cache = Hash.new { |hash, key| hash[key] = "made-" + key }
cache["a"]        # "made-a"  (the proc runs and stores)
cache.size        # 1         (the proc inserted the entry)
cache["a"]        # "made-a"  (now present, the proc does not run again)
```

A default value is returned without inserting it, so repeated misses keep the
hash empty and always return the same default. A default proc is invoked with the
hash and the missing key; it inserts an entry only if its body assigns one
(`hash[key] = ...`). A proc that merely returns a value leaves the hash unchanged:

```vibe
computed = Hash.new { |hash, key| "computed-" + key }
computed["x"]     # "computed-x"
computed.size     # 0   (the proc did not store)
```

Read the configured default with `default` and `default_proc`:

- `default` with no argument returns the configured default value, or `nil`. Like
  Ruby, it never runs the default proc, so a proc-only hash reports `nil` here.
- `default(key)` resolves the default the same way a missing-key `[]` access
  would: a default proc is invoked with `(hash, key)` (and may store), otherwise
  the default value is returned.
- `default_proc` returns the configured default proc, or `nil`.

```vibe
Hash.new(0).default                 # 0
Hash.new(0).default(:any)           # 0
Hash.new { |h, k| 1 }.default       # nil
Hash.new { |h, k| k }.default(:x)   # :x  (proc invoked with the key)
Hash.new { |h, k| 1 }.default_proc  # the proc
{}.default                          # nil
{}.default_proc                     # nil
```

Only `[]` access consults the default. `fetch`, `dig`, and `values_at` ignore it,
matching Ruby for `fetch` and `dig`:

```vibe
Hash.new(0).fetch(:missing, 99) # 99 (fetch's own default, not the hash default)
Hash.new(0).dig(:missing)       # nil
```

The default travels with the hash object: index assignment (`hash[key] = ...`)
keeps it, and `merge` (with its `update` / `merge!` aliases) copies the
receiver's default onto the merged hash. Every other transform that returns a new
hash (`select`, `reject`, `slice`, `except`, `transform_keys`,
`transform_values`, `compact`, `store`, `replace`, ...) returns a plain hash with
no default, so derived hashes do not silently inherit missing-key behavior.

```vibe
base = Hash.new(0)
base.merge({ a: 1 })[:b]        # 0   (default preserved)
base.select { |k, v| true }[:b] # nil (default dropped)
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
- `fetch_values(*keys)` returns the values for several keys at once, in the
  requested order. Unlike `fetch`, it raises when a key is absent. Pass a block
  to compute a replacement for each missing key instead of raising.
- `dig(*path)` for nested lookup.
- `values_at(*keys)` to read several values at once, in requested key order, with
  `nil` for missing keys.

```vibe
def display_name_or_default(records, player_id)
  fallback = "unknown"
  records.dig(player_id, :meta, :display_name) || fallback
end
```

```vibe
{ a: 1, b: 2 }.values_at(:b, :c, :a)           # [2, nil, 1]
{ a: 1, b: 2 }.fetch_values(:a, :b)            # [1, 2]
{ a: 1 }.fetch_values(:a, :missing)            # raises "key not found: :missing"
{ a: 1 }.fetch_values(:a, :missing) { |k| k }  # [1, :missing]
```

## Transform and filter helpers

- `merge(*others)` combines the receiver with any number of hashes into a new
  hash. Later hashes win on key conflicts, and an optional block resolves
  conflicts by yielding `(key, old_value, new_value)`. Called with no arguments
  it returns a copy of the receiver.
- `update(*others)` / `merge!(*others)` are aliases of `merge`. Ruby mutates the
  receiver in place; Vibescript's method-based helpers are immutable-style, so
  all three return a new merged hash and leave the receiver unchanged.
- `replace(other)` returns a new hash holding `other`'s entries, discarding the
  receiver's own. Ruby mutates the receiver in place; this immutable-style
  version leaves it unchanged.
- `flatten(depth = 1)` returns a flat array of the entries. At the default depth
  the result is `[key, value, ...]`; values that are arrays are kept nested
  unless a deeper `depth` is given. A `depth` of `0` returns the `[key, value]`
  pairs nested, and a negative `depth` flattens completely.
- `store(key, value)` returns a new hash with the key assigned, leaving the
  receiver unchanged. Like the other method-based helpers it is immutable-style;
  use index assignment (`hash[key] = value`) when you want to mutate in place.
- `compact` removes `nil` values.
- `slice(*keys)` keeps only selected keys. Candidate keys that are absent are
  omitted, and keys whose type cannot be a hash key (anything other than a symbol
  or string) are treated as misses rather than raising, so `slice` with only
  unmatched candidates returns an empty hash.
- `except(*keys)` removes selected keys. Keys whose type cannot be a hash key
  (anything other than a symbol or string) are treated as misses and ignored, so
  the surrounding entries are preserved.
- `select` / `reject` with a block.
- `transform_keys` / `transform_values` with a block.
- `deep_transform_keys` for recursive key mapping across nested hashes/arrays.
- `remap_keys(mapping_hash)` for direct key rename maps.

The map-producing transforms run inside the sandbox. Before building a derived
map they project its size against the memory quota, so a transform over a large
hash is rejected up front rather than after the backing map is allocated. While
walking the receiver they charge the step quota per entry and honor context
cancellation, so large materializations stay bounded. This applies to `merge`
(and its `update` / `merge!` aliases), `replace`, `store`, `compact`, `slice`,
`except`, `select`, `reject`, `transform_keys`, `transform_values`, and
`remap_keys`.

The block-driven transforms (`transform_keys`, `transform_values`, and the
`merge` conflict block) also charge what a block produces against the memory quota
as it is produced, so fresh content accumulated in the result cannot exceed the
quota before the build completes. `transform_values` and the `merge` conflict
block charge each block-returned *value* at its full payload; `transform_keys`
charges each block-synthesized *key* (its value stays a receiver value already
counted, so only the fresh key is new). This block-result charge is
*conservative*: each result is counted as it is inserted, deduplicated only
against other block results and never against the receiver or argument values --
those are already counted once in the call's live footprint, so they are written
to the output map slots without being re-measured. A block that returns a value
unchanged and shared with the receiver -- or that collapses several writes onto
one key -- is therefore counted at full size rather than deduplicated away. This
over-count is deliberate: it keeps the bound sound even when a block mutates a
receiver-owned container in place (for example appending into an array that still
has spare capacity) and returns it, a case where deduplicating against the
receiver would charge the fresh payload nothing and let it escape the quota. The
array-side equivalent of this accounting is tracked in #787.

Two helpers are not yet bounded this way:

- `deep_transform_keys` does not bound its recursive materialization against the
  sandbox limits (tracked in #786).
- `flatten` materializes a sorted key list and a `[key, value, ...]` array
  without a projected memory check or per-entry step charge; it is grouped with
  the array-materialization work alongside `keys` and `values`.

Apply both only to inputs of known size.

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

`keys`, `values`, `flatten`, and block-based hash iteration process entries in
sorted key order for deterministic behavior.

Example:

```vibe
def merge_defaults(player)
  defaults = { goal: 5000, raised: money("0.00 USD") }
  defaults.merge(player)
end
```

Review `examples/hashes/` for live scripts used by the tests.
