- **Hardened hash transform sandbox accounting.** `Hash#merge`, `Hash#update`,
  `Hash#merge!`, `Hash#replace`, `Hash#store`, `Hash#except`, `Hash#slice`,
  `Hash#compact`, `Hash#remap_keys`, `Hash#select`, `Hash#reject`,
  `Hash#transform_keys`, `Hash#transform_values`, and `Hash#deep_transform_keys`
  now project the size of the derived map against the memory quota before
  reserving it, so a transform over a large hash is rejected up front instead of
  after a full output map has already been allocated. The blockless transforms
  charge the step quota per entry and honor context cancellation while walking the
  receiver. Block-driven transforms (`Hash#transform_keys`,
  `Hash#transform_values`, `Hash#deep_transform_keys`, and the `Hash#merge`
  conflict block) additionally charge each block result against the quota as it is
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
  reserved, so it cannot escape the sandbox limit on a large receiver.
