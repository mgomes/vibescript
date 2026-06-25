- **Added: Ruby-style `Array#dig` and mixed hash/array `dig` paths.** `dig`
  now descends one level per path component across both collection kinds: an
  integer index into an array or a symbol/string key into a hash, so a single
  `dig` can walk JSON-shaped data, e.g. `[[1, 2], [3, 4]].dig(1, 0)` returns
  `3` and `{ a: [10, 20] }.dig(:a, 1)` returns `20`. Missing keys and
  out-of-range indexes yield `nil` rather than raising, while indexing an array
  with a non-integer component raises, matching how arrays reject non-integer
  indexes elsewhere.
