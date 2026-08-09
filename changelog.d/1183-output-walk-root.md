- **Fixed: a builtin's accumulated results are now visible to the memory quota
  while its block runs.** `Hash#fetch_values`, `Hash#values_at`, `hash.map`,
  `array.map`, `Hash#map_with_index` and `Hash#transform_values` build their
  result in a Go local the estimator had no root for, so every check performed
  inside the block or default proc measured a graph missing everything the loop
  had already retained: a callback returning an individually permitted value
  could accumulate past `MemoryQuotaBytes` one accepted result at a time. Those
  outputs are now registered as base-walk roots, so each check re-derives what
  the output holds at the moment it runs and deduplicates it against the
  receiver and the arguments. Results that alias memory the walk already reaches
  are no longer charged twice, which the `hash.map` and `array.map` scratch
  reservations did. Costs are unchanged: the roots are memoized alongside the
  reachable graph, and `VIBES_ESTIMATOR_VERIFY` re-derives every commit from
  scratch.
