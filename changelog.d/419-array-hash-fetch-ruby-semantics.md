- **Changed: `Array#fetch` and `Hash#fetch` now follow Ruby's strict
  missing-value contract.** A missing index or key with no fallback raises
  (`array.fetch index N outside of array bounds: ...` and `hash.fetch key not
  found: KEY`) instead of returning `nil`. Both forms now evaluate a Ruby-style
  block default, calling it with the requested index or key when the value is
  absent (`[1, 2, 3].fetch(9) { |i| i + 10 }` returns `19`). When both a default
  argument and a block are supplied, the block supersedes the default and is
  evaluated on a miss, matching Ruby (`[].fetch(0, 7) { 9 }` returns `9`).
  `Array#fetch` also accepts negative
  indices, counting from the end like `at`. For nil-on-miss array lookups use
  `[]`, `at`, `slice`, or `dig` (array `[]` counts negative indices from the end
  and returns `nil` out of range, like Ruby's `Array#[]`); for hashes use `[]`
  or `dig`. Use `fetch` when a miss should raise instead.
