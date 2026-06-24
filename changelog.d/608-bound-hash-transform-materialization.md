- **Hardened hash transform sandbox accounting.** `Hash#merge`, `Hash#update`,
  `Hash#merge!`, `Hash#replace`, `Hash#store`, `Hash#except`, `Hash#slice`,
  `Hash#compact`, `Hash#remap_keys`, `Hash#select`, `Hash#reject`,
  `Hash#transform_keys`, and `Hash#transform_values` now project the size of the
  derived map against the memory quota before reserving it, so a transform over a
  large hash is rejected up front instead of after a full output map has already
  been allocated. The blockless transforms charge the step quota per entry and
  honor context cancellation while walking their entries; `Hash#replace` charges
  a step per copied entry as it adopts the replacement's contents, so replacing
  with a large replacement hash is bounded by the step quota and cancellation
  rather than copying the whole replacement after the sandbox should have
  stopped. Block-driven transforms
  (`Hash#transform_keys`, `Hash#transform_values`, and the `Hash#merge` conflict
  block) additionally charge each block result against the quota as it is
  produced, so fresh values accumulated in the output cannot exceed the memory
  quota before the build completes. This block-result charge is accounted
  *conservatively*: each block result's full payload is counted as the result is
  inserted, deduplicated only against other block results, never against the
  receiver or other call roots. Only block-produced content reaches this
  estimator: `Hash#merge` copies its receiver and non-conflict argument entries
  straight into the output map without charging them through it (their payloads are
  already counted in the call's live footprint), and `Hash#transform_keys` charges
  only the fresh key it synthesizes while leaving the retained receiver value
  uncharged. Seeding the estimator with a receiver or argument value would let a
  conflict block that mutates and returns that value in place be deduplicated to
  nothing, under-counting its fresh payload. A block that returns a value unchanged
  and shared with the receiver (or that collapses several writes onto one key) is
  therefore over-counted rather than deduplicated away. This is deliberate: deduplicating a
  block result against the baseline is unsound when a block mutates a
  receiver-owned container in place (for example appending a large value into an
  array that still has spare capacity) and returns it -- the container's backing
  was already in the baseline, so dedup would charge the fresh payload nothing and
  let it escape the quota. Counting each result at its full current size never
  under-counts the live result footprint, keeping the sandbox bound sound under
  in-place mutation; the array-side equivalent of this accounting is tracked in
  #787. `Hash#store` sizes its projection by the
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
  projection, keeping the combined output-plus-scratch peak bounded. Those
  transforms preallocate their output with the receiver's size, so the full output
  backing is live before the first block runs; that backing is now reserved against
  the quota up front (matching the bytes the up-front projection charges) rather
  than charged one slot at a time as entries are written, so a large *early* block
  result is checked against the whole live backing instead of only the slots filled
  so far and cannot transiently exceed the quota before later entries are added.
  `Hash#each`, `Hash#each_key`, and `Hash#each_value` build no derived map -- they
  return the receiver -- so they no longer reserve an output map they never
  allocate, and a quota that exactly fits the receiver and the scratch buffer
  admits the walk instead of being falsely rejected. The conservative block-result
  charge is cycle-safe by construction: a block can return a value that reaches
  itself through in-place index assignment (`a = [0]; a[0] = a`), and the memory
  estimator's own seen-sets terminate the walk within a single value, so a cyclic
  result is charged a finite amount once. `Hash#except` now also charges the exclusion set it
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
