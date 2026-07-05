- **Fixed: `Array#flatten` now participates in sandbox accounting while its
  result is being built.** Flatten's output length cannot be cheaply bounded up
  front (each receiver slot can expand into arbitrarily many leaves), so the
  build now charges one step per element examined — running the periodic memory
  and context checks — and charges the output backing's growth before each
  doubling is allocated. An oversized flatten is rejected before the over-quota
  backing exists, instead of after the full result was allocated natively, and
  a canceled context stops the walk mid-build. The recursion also builds into a
  single shared output slice (for `Hash#flatten` too), replacing the
  per-nesting-level slices the old merge-upward implementation allocated.
  Results are unchanged for in-quota calls. Focused quota coverage now pins
  `join`, fixed-size `window`, and `flatten` in both directions: an in-quota
  call succeeds byte-identically and an oversized call fails with the quota
  error before the result materializes.
