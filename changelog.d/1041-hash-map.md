- **Added: `Hash#map`.** Hashes had `map_with_index` but no `map`, so every hash
  aggregation had to route through `to_a` and positional pair indexing. It yields
  the `[key, value]` pair, so `h.map { |k, v| ... }` and `h.map { |pair| ... }`
  both behave as in Ruby.
