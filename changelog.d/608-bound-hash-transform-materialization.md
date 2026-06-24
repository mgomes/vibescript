- **Hardened hash transform sandbox accounting.** `Hash#merge`, `Hash#update`,
  `Hash#merge!`, `Hash#replace`, `Hash#store`, `Hash#except`, `Hash#slice`,
  `Hash#compact`, `Hash#remap_keys`, `Hash#select`, `Hash#reject`,
  `Hash#transform_keys`, and `Hash#transform_values` now project the size of the
  derived map against the memory quota before reserving it, so a transform over a
  large hash is rejected up front instead of after a full output map has already
  been allocated. The blockless transforms charge the step quota per entry and
  honor context cancellation while walking the receiver. Block-driven transforms
  (`Hash#transform_keys`, `Hash#transform_values`, and the `Hash#merge` conflict
  block) additionally charge each block result against the quota as it is
  produced, so fresh values accumulated in the output cannot exceed the memory
  quota before the build completes. `Hash#store` sizes its projection by the
  existing-key case, so replacing a key no longer over-reports the result size, and
  `Hash#except` projects against the receiver before building its exclusion set so
  a huge candidate-key list against a tiny receiver fails fast. Block-driven hash
  methods (`Hash#each`, `Hash#each_key`, `Hash#each_value`, `Hash#select`,
  `Hash#reject`, `Hash#transform_keys`, and `Hash#transform_values`) now charge the
  step quota per entry directly rather than relying on the block body, so an empty
  block still consumes steps and observes cancellation instead of walking the
  receiver unbounded. The sorted key scratch buffer these transforms allocate to
  iterate deterministically is now charged against the memory quota before it is
  reserved, so it cannot escape the sandbox limit on a large receiver; for the
  block-driven transforms that buffer stays live while the output map fills, so it
  is held against the quota for the whole build rather than only at the up-front
  projection, keeping the combined output-plus-scratch peak bounded.
  `Hash#each`, `Hash#each_key`, and `Hash#each_value` build no derived map -- they
  return the receiver -- so they no longer reserve an output map they never
  allocate, and a quota that exactly fits the receiver and the scratch buffer
  admits the walk instead of being falsely rejected. When a transform overwrites a
  key it already holds -- a `Hash#merge` conflict block returning the old value, or
  a `Hash#transform_keys` block collapsing several input keys onto one output key --
  the incremental accounting now reference-counts the output's payload backings and
  releases only the bytes the swap leaves unreachable, so a still-reachable value
  (the returned old value, or one a fresh result wraps) is never un-charged. This
  keeps the running total from dropping below the map's true live footprint, which
  could otherwise let later inserts materialize past the quota. The reference-count
  walk is cycle-safe: a block can return a value that reaches itself through
  in-place index assignment (`a = [0]; a[0] = a`), and charging then releasing such
  a value is now mirror-symmetric, so repeatedly replacing a key that holds a cyclic
  value keeps the running total constant instead of inflating it on every write and
  falsely tripping the quota. The incremental walk now charges a value's payload
  exactly as the memory estimator would (the two derive every backing's structural
  cost from one shared computation), closing an undercount where a block returning
  a fresh hash of scalar values omitted the per-entry value slots and could
  accumulate past the quota. `Hash#except` now also charges the exclusion set it
  builds from the candidate keys present in the receiver, which is live alongside
  the copied output at peak, so `h.except(*h.keys)` over a large receiver can no
  longer allocate that set plus the full output past a receiver-plus-output quota.
  A bare
  `Hash#merge { ... }` with no argument hashes returns a copy of the receiver
  without running the block, so it no longer charges the conflict block's base
  scratch buffer it never allocates, and a large receiver whose copy fits the quota
  is admitted instead of being falsely rejected for that phantom scratch.
  `Hash#deep_transform_keys` is intentionally left unbounded for now; bounding its
  recursive materialization is tracked separately in #786.
