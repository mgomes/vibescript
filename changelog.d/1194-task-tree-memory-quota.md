- **Fixed: `MemoryQuotaBytes` now bounds a chain of nested tasks rather than
  each level of it.** Every nested task level runs on an `Execution` of its own,
  and each one read the host's whole memory quota fresh from the engine config,
  so live memory multiplied with nesting depth while every individual level
  looked permitted. Measured against an 8 MiB quota with 2 MiB held live per
  level, a slotted chain — 64 distinct goroutines, nothing running inline —
  reached 63 levels holding 130 MiB, 16x what the host configured, refused by
  nothing at all. The inline and slotted shapes were byte-for-byte identical at
  equal depth, so bounding inline depth could not fix it: that cap stopped the
  inline chain at 16 levels for a reason that had nothing to do with memory. A
  nested level is now charged against a ceiling shared with its ancestors, so
  the quota means what the host asked for however deep the nesting goes.
- **Changed: deeply nested task chains now report a memory limit sooner.** This
  is the point of the change, but it is a behaviour change: a chain that used to
  run because each level got the whole allowance again now fails with `memory
  quota exceeded` once the levels hold more between them than the host allows. A
  level is charged only what it holds *beyond* the structure it inherited, so
  globals and modules that every level can reach are counted once for the chain
  rather than once per level — summing whole per-level estimates would have
  charged 68 MiB for a single 4 MiB global read at seventeen levels. The charge
  does not grow with depth: a 1 MiB global read at 32 levels needs 2.03 MiB,
  against 2.01 MiB at one level, where a naive sum would need 33 MiB.
- **Unchanged: the width of a flat `Tasks.map`.** Only the ancestor chain
  aggregates; siblings are not charged for one another. Bounding width too would
  make the quota literally true for the whole call, but it would refuse a
  64-wide map of 1 MiB workers that works today — charging 256 MiB for a script
  that really peaks at 4 MiB — and width is already bounded by
  `MaxTaskConcurrency`, which a host sets deliberately. Whether a host should
  get a separate tree-level ceiling is a public API question, filed separately.
  The remaining multiplier on live memory is therefore the worker pool rather
  than the pool times the nesting depth.
