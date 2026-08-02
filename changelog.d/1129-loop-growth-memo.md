- **Performance: building collections in a hand-written loop is no longer
  quadratic under a memory quota.** Growing an array with `<<` or filling a
  hash with new keys invalidated the memory estimator's base-walk memo every
  iteration — the append's epoch bump, the hash literal's construction-time
  reference walk and its three epoch bumps, and the capacity-doubling
  reservation each forced whole-graph re-walks, so a loop that builds n
  records did O(n²) estimator work (the quota is on by default at 16 MiB).
  Literals now build epoch-silently and price their entries through the
  memoized walk, growth reservations resume the memo, and eligible appends
  and added hash entries commit their marginal bytes into the memo instead of
  discarding it. Building 2,000 `{id:, name:}` records under a 64 MiB quota
  drops from 2.30s to 6.6ms and scales linearly. Byte totals are unchanged:
  the smallest admitting quota is pinned identical to the uncached reference
  walk, and `VIBES_ESTIMATOR_VERIFY` re-derives every incremental commit from
  scratch. Loops whose bodies call builtins (a `j.to_s` key, `push`) still
  discard the memo at builtin depth and stay super-linear — that contributor
  is tracked separately.
