- **Added: Ruby-style collection deletion and insertion helpers.** Arrays gain
  `delete`, `shift`, `unshift`, and `insert`, and hashes gain `delete`. Because
  Vibescript collections are non-mutating, the removal helpers return both halves
  of the result like the existing `Array#pop`: `Array#delete(value)` returns
  `{ array:, deleted: }` (reporting the value when found, `nil` otherwise, or a
  block result on a miss), `Array#shift` / `shift(n)` returns `{ array:, shifted: }`,
  and `Hash#delete(key)` returns `{ hash:, deleted: }` (with the same block form
  for misses). `Array#unshift` is a Ruby-style alias for `prepend`, and
  `Array#insert(index, *values)` returns a new array with the values inserted
  before `index`, following Ruby's negative-index and past-the-end padding rules.
