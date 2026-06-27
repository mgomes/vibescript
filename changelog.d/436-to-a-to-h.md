- **Added: Ruby-style `Hash#to_a` and `Array#to_h` conversion helpers.**
  `Hash#to_a` returns the `[key, value]` pairs in Vibescript's deterministic
  sorted-key order (the inverse of `Array#to_h`, equivalent to `flatten(0)`).
  `Array#to_h` builds a hash from an array of two-element `[key, value]` pairs,
  converting keys through the same symbol/string hash-key rules used elsewhere
  and keeping the last pair on duplicate keys. A block form
  `to_h { |element| [key, value] }` maps each element to its pair. Malformed
  input raises: a non-array element, a pair that is not exactly two elements, or
  a key that is not a symbol or string.
