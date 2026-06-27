- **Changed: `Array#fetch` and `Hash#fetch` now follow Ruby's strict
  missing-value contract.** A missing index or key with no fallback raises
  (`array.fetch index N outside of array bounds: ...` and `hash.fetch key not
  found: KEY`) instead of returning `nil`. Both forms now evaluate a Ruby-style
  block default, calling it with the requested index or key when the value is
  absent (`[1, 2, 3].fetch(9) { |i| i + 10 }` returns `19`). Supplying both a
  default argument and a block is rejected. `Array#fetch` also accepts negative
  indices, counting from the end like `at` and `[]`. Use `[]` or `dig` when a
  missing value should yield `nil` rather than raise.
