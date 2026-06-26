- **Fixed: `Hash#each` yields the key/value pair to single-parameter blocks like
  Ruby.** A block declaring one positional parameter now receives each entry as a
  two-element `[key, value]` array instead of only the key, so
  `{ a: 1 }.each { |pair| pair }` yields `[:a, 1]`. Blocks with two parameters
  still receive the key and value separately, extra parameters still receive
  `nil`, and a single destructuring parameter such as `|(key, value)|` unpacks the
  pair.
- **Fixed: `Array#reduce` charges its accumulator against the memory quota.** A
  fold whose block destructures the accumulator with a rest target, such as
  `reduce(big) do |(head, *tail), item| ... end`, copies part of the live
  accumulator into a fresh backing. The accumulator lives only on the runtime's
  Go stack and evolves every call, so it was missing from the per-call memory
  accounting; the fold now charges the current accumulator on each call, closing
  a path that could allocate past the sandbox quota.
