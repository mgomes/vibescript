- **Fixed: `Hash#except` ignores unsupported key types like Ruby misses.**
  `Hash#except` no longer raises when given an argument whose type cannot be a
  hash key (anything other than a symbol or string). Because Vibescript hash keys
  are only symbols or strings, such an argument can never match an entry, so it is
  now treated as a Ruby-style miss and ignored. Mixed argument lists still exclude
  the supported keys, so `{ a: 1 }.except(1)` returns `{ a: 1 }` while
  `{ a: 1 }.except(1, :a)` returns `{}`.
