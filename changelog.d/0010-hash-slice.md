- **Fixed: `Hash#slice` omits unsupported candidate keys like Ruby misses.**
  `Hash#slice` no longer raises when given a candidate key whose type cannot be a
  hash key (anything other than a symbol or string). Such a candidate can never
  match an entry, so it is treated as a Ruby-style miss and dropped from the
  result instead of failing. Mixed argument lists still keep the supported keys,
  so `{ a: 1, b: 2 }.slice(:a, 1)` returns `{ a: 1 }` while
  `{ a: 1 }.slice(1)` and `{ a: 1 }.slice` both return `{}`.
