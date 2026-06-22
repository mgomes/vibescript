- **Added: Ruby-style hash member, value, and store helpers.** `Hash#member?`
  joins `key?`/`has_key?`/`include?` as a key-membership alias, `Hash#value?` and
  `Hash#has_value?` report value membership using the same `==` equality as the
  rest of the language, and `Hash#store(key, value)` returns a new hash with the
  key assigned. Like the other method-based hash helpers, `store` is
  immutable-style and leaves the receiver unchanged.
