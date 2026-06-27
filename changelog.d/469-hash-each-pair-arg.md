- **Fixed: `Hash#each` yields the key/value pair to single-parameter blocks like
  Ruby.** A block declaring one positional parameter now receives each entry as a
  two-element `[key, value]` array instead of only the key, so
  `{ a: 1 }.each { |pair| pair }` yields `[:a, 1]`. Blocks with two parameters
  still receive the key and value separately, extra parameters still receive
  `nil`, and a single destructuring parameter such as `|(key, value)|` unpacks the
  pair.
- **Fixed: `Array#reduce` charges its accumulator against the memory quota
  without double counting.** A fold whose block destructures the accumulator with
  a rest target, such as `reduce(big) do |(head, *tail), item| ... end`, copies
  part of the live accumulator into a fresh backing. The accumulator lives only on
  the runtime's Go stack and evolves every call, so it was missing from the
  per-call memory accounting; the fold now charges the current accumulator on each
  call, closing a path that could allocate past the sandbox quota. A reduce with no
  initial value makes the accumulator the receiver's first element, which is already
  counted in the receiver, so the accumulator charge now deduplicates against the
  receiver: a large first element is charged once, not twice, so a quota that fits
  the real peak is no longer wrongly rejected.
- **Fixed: rest-collecting block parameters reject an over-quota tail before
  allocating it.** A block such as `[[huge...]].each { |(head, *tail)| }` copies the
  collected tail into a fresh backing slice when it binds `tail`. The bind charge now
  preflights that window against the memory quota before the copy, so a quota smaller
  than a single copied tail rejects the walk before the backing is materialized
  instead of allocating the whole tail first and only then reporting the overflow.
- **Fixed: block-driven hash transforms count their output map in the rest-bind
  charge.** `Hash#select`, `#reject`, `#transform_keys`, `#transform_values`, and a
  block-conflict `#merge` hold their preallocated output map and sorted-key scratch
  while the block binds a rest-collecting destructure parameter such as
  `|k, (head, *tail)|`. Those buffers are now reserved against the memory quota before
  the block runs, so the rest-bind charge measures the fresh tail copy on top of them
  rather than against a baseline that omitted them, closing a path where the combined
  peak could exceed the quota.
