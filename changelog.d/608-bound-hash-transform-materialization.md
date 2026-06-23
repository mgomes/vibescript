- **Hardened hash transform sandbox accounting.** `Hash#merge`, `Hash#update`,
  `Hash#merge!`, `Hash#replace`, `Hash#store`, `Hash#except`, `Hash#slice`,
  `Hash#compact`, `Hash#remap_keys`, `Hash#select`, `Hash#reject`,
  `Hash#transform_keys`, and `Hash#transform_values` now project the size of the
  derived map against the memory quota before reserving it, so a transform over a
  large hash is rejected up front instead of after a full output map has already
  been allocated. The blockless transforms also charge the step quota per entry
  and honor context cancellation while walking the receiver, keeping large
  materializations bounded.
