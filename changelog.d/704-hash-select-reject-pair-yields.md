- **Fixed: `Hash#select` and `Hash#reject` now yield Ruby-style pairs.** A block
  with a single parameter receives each entry as a two-element `[key, value]`
  pair instead of just the key, so predicates ported from Ruby such as
  `select { |pair| pair[1] == 1 }` work as written. Two block parameters still
  bind the key and value separately, and extra parameters bind to `nil`. The
  rule is shared with the other hash iteration helpers through a single
  block-binding path.
