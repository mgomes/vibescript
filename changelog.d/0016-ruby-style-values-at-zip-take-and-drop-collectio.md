- **Added: Ruby-style `values_at`, `zip`, `take`, and `drop` collection helpers.**
  `Hash#values_at(*keys)` returns values in requested key order with `nil` for
  missing keys. `Array#zip(*arrays)` combines arrays element-wise into rows keyed
  to the receiver's length, padding short arrays with `nil` and rejecting
  non-array arguments. `Array#take(n)` and `Array#drop(n)` return prefix and
  suffix slices without mutating the receiver, truncating fractional counts like
  Ruby's `to_int` conversion and rejecting negative counts.
