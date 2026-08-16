- **Changed: hashes use one string keyspace.** Strings and symbols are the only
  accepted hash keys, a symbol normalizes to its string, and `hash["name"]`,
  `hash[:name]` and the label `name:` all address one entry, so `keys` returns
  strings and a JSON round trip no longer changes lookup behavior. Every other
  key type is rejected. Arbitrary keys made the memory boundary a graph problem
  -- recursive canonicalization, cycle detection and per-occurrence accounting
  for composite keys -- while the separate string and symbol keyspaces made
  ordinary JSON-shaped data behave differently depending on which spelling built
  it ([ADR-006](docs/adr/006-slim-language-for-predictable-sandboxing.md)).
  Insertion order, `nil` on a missing `[]`, `fetch` fallbacks, and any
  Vibescript value as a hash *value* are unchanged.

  Migration: convert a computed key explicitly, usually with `to_s`
  (`counts[id.to_s]`); the rejection names the conversion. `group_by` and
  `tally` produce keys the same way, so `words.group_by { |w| w.size }` becomes
  `words.group_by { |w| w.size.to_s }`. Code that relied on `:name` and `"name"`
  being different entries now has one entry holding the later write, and a
  literal such as `{ name: 1, "name": 2 }` is a single entry. A block that
  compares a yielded key against a symbol (`if key == :id`) must compare against
  the string (`if key == "id"`); symbols are unchanged as values, only keys
  normalize. `hash<symbol, V>` keeps working and describes the same hash as
  `hash<string, V>`.

- **Removed: per-hash default values.** `Hash.new` takes no argument and no
  block, and `hash.default` / `hash.default_proc` are gone. A default stored on
  a hash made every missing-key read a potential script callback -- with its own
  effects, accounting and type-checking surface -- for a fallback the call site
  can state directly.

  Migration: replace `Hash.new(0)` with `{}` and read misses through
  `hash.fetch(key, 0)`, which supplies the fallback per lookup without inserting.
  Replace a default proc that filled the hash with an explicit store where the
  miss is handled (`cache[key] = build(key) if cache[key].nil?`). `dig` and
  `values_at` now contribute `nil` for a missing key rather than consulting a
  stored default.

- **Fixed: a hash store and a hash transform charged inconsistent memory.** The
  charged-commit path for `hash[key] = value` added an entry slot when the write
  grew the hash past its reserved capacity, when that slot is only ever consumed
  rather than added, so a store loop drifted above the reference walk. The final
  admission before a hash transform allocated its output also missed the
  insertion-order backing that the matching reservation charges, so it admitted
  outputs the reservation then refused. Both now agree with the reference walk.
