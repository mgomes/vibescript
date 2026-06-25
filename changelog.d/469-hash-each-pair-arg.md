- **Fixed: `Hash#each` yields the key/value pair to single-parameter blocks like
  Ruby.** A block declaring one positional parameter now receives each entry as a
  two-element `[key, value]` array instead of only the key, so
  `{ a: 1 }.each { |pair| pair }` yields `[:a, 1]`. Blocks with two parameters
  still receive the key and value separately, extra parameters still receive
  `nil`, and a single destructuring parameter such as `|(key, value)|` unpacks the
  pair.
