- **Performance: iterating a host-supplied hash no longer degrades under a
  memory quota.** `each`, `each_key`, `each_value`, `select`, `reject`, and
  `transform_values` re-measured the whole receiver on every check when the hash
  came from the host rather than from a script, so the walk cost grew
  quadratically with the entry count. Iterating a 600-entry hash this way is now
  roughly 50x faster.
