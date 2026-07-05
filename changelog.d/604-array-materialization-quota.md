- **Fixed: `Array#flatten` and `Hash#flatten` now participate in sandbox
  accounting while their results are being built.** Flatten's output length
  cannot be cheaply bounded up front (each receiver slot can expand into
  arbitrarily many leaves), so both builds now charge one step per element
  examined — running the periodic memory and context checks — and charge the
  output backing's growth before each doubling is allocated. `Hash#flatten`
  additionally meters its `[key, value]` pair pre-build the way `Hash#to_a`
  does: the entry/key scratch and the pairs backing are charged before the sort
  or any pair allocation, and each appended pair charges a step plus its
  structures. Both methods previously charged ~0 steps, so large flattens now
  consume step quota proportional to the elements they examine. An oversized
  flatten is rejected before the over-quota backing exists, instead of after
  the full result was allocated natively, and a canceled context stops the walk
  mid-build. The recursion also builds into a single shared output slice,
  replacing the per-nesting-level slices the old merge-upward implementation
  allocated. Results are unchanged for in-quota calls. Focused quota coverage
  now pins `Array#join`, fixed-size `Array#window`, `Array#flatten`, and
  `Hash#flatten` in both directions: an in-quota call succeeds byte-identically
  and an oversized call fails with the quota error before the result
  materializes.
