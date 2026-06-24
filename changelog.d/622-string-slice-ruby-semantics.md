- **Improved: Ruby-style `String#slice` selectors.** `slice` now accepts the
  same selector shapes as Ruby beyond the previous non-negative integer start
  with optional length. A negative integer index counts back from the end; a
  range returns the matching substring with Ruby-compatible negative bounds; and
  a substring argument returns that substring when it is contained, otherwise
  `nil`. The `slice(start, length)` form now also supports a negative start.
  Selectors that fall outside the string return `nil`, and indexing stays
  rune-aware.
