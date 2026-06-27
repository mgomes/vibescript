- **Added: Ruby-style index-aware iteration helpers.** Arrays gain
  `each_with_index { |item, index| }` (returns the receiver) and
  `map_with_index { |item, index| }` (returns a new array of block results),
  both passing each element's 0-based index to the block. Hashes gain matching
  `each_with_index { |pair, index| }` and `map_with_index { |pair, index| }`
  helpers that yield each `[key, value]` pair plus its index in sorted key
  order, mirroring Ruby's `Hash#each_with_index`. All four take no arguments,
  require a block, and run under the sandbox step and memory quotas.
