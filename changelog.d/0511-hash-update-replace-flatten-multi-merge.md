- **Added: Ruby-style `Hash#update`, `Hash#merge!`, `Hash#replace`, `Hash#flatten`,
  and multi-hash `Hash#merge`.** `Hash#merge` now accepts any number of hashes
  (applied left to right, so later hashes win on conflicts) and returns a copy of
  the receiver when called with no arguments; the optional conflict block folds
  through each argument in turn. `Hash#update` and `Hash#merge!` are aliases of
  `merge`, and `Hash#replace` adopts another hash's entries. Like the other
  method-based hash helpers these are immutable-style: they return a new hash and
  leave the receiver unchanged rather than mutating in place. `Hash#flatten(depth = 1)`
  returns the entries as a flat array, defaulting to `[key, value, ...]`, with
  `0` keeping the `[key, value]` pairs nested and a negative depth flattening
  completely. Entries are emitted in sorted key order.
