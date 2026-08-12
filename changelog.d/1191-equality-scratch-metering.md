- **Fixed: comparing hashes no longer costs quadratic host CPU under a memory
  quota.** Validating an equality walk's sort scratch ran a reachable-graph
  estimate before every allocation, and every check a builtin drives bypasses the
  base-walk memo, so each compared hash paid a full uncached whole-graph walk to
  place 24 bytes of key slice. `array.include?` and its siblings charge one step
  per candidate, so probing an array of n small hashes ran n uncached graph walks
  for n charged steps: quadratic host work under a linear step budget, on a
  process shared with other tenants. Probing 800 one-entry hashes falls from
  4,154,117 estimator node visits to 302,917, and from 498ms to 36ms at 1600
  candidates. Scratch is now counted for as long as a walk holds it, so the
  periodic quota check and every call-root and admission estimator account for it,
  and it is repriced against a granule derived from the configured quota rather
  than a constant sized for the default profile.

  **Known residual, relevant if you configure a small `MemoryQuotaBytes`.** A
  single comparison may reach up to one granule of transient sort scratch — a
  256th of the configured quota, so 64 KiB under the default 16 MiB profile and
  proportionally smaller on smaller quotas — before that footprint is validated
  against the quota. The scratch is released when the comparison ends and does not
  accumulate across comparisons, so the peak exposure is one such window rather
  than a sum, and it cannot grow without bound. Closing the window entirely would
  mean validating at every comparison, which measures at 15.7x the estimator work
  of the equivalent unvalidated scan and reinstates the quadratic cost above.
  Hosts sizing a quota tightly against physical memory should leave that window as
  headroom.
