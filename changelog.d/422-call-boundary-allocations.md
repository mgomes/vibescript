- **Improved: call-boundary copying for large host data payloads.** Data-only
  host argument graphs (scalars plus alias-free arrays, hashes, and objects)
  now deep-copy through a tight copier instead of the full rebind walk at
  `Script.Call` entry, composite `CallOptions.Globals` bind lazily and are
  cloned only when the script (or an inheriting task) actually reads them, and
  the contracted capability boundary clones scalar-only row maps wholesale.
  Isolation is unchanged: every host value a script can reach is still a
  per-call deep copy, capability grants captured by escaped closures are still
  revoked on re-entry, and `StrictEffects` still validates globals eagerly at
  bind time. An unused large global no longer pays its full clone (~24x less
  wall time in `BenchmarkTasksMapUnusedLargeGlobal`); large payload calls
  spend less on rebind bookkeeping and capability-boundary cloning. (#422,
  #366, #353)
