- **Performance: memory-quota checks memoize the estimator's base walk.** The
  estimator's reachable-graph walk is now memoized per execution and reused by
  consecutive checks while a process-wide mutation epoch (bumped by every value
  wrapper mutator, environment write, and builtin dispatch) and the execution's
  root-set topology prove the graph unchanged, so per-statement, argument, and
  call-boundary checks around a large stable payload no longer each re-pay a
  full graph walk. Estimates are byte-identical to the unmemoized walk — the
  memoized check resumes the exact same deduplicated computation — so quota
  pass/fail thresholds do not move; large capability payload calls
  (`BenchmarkCapabilityContractLargeArgs/rows_10000`) run about 2x faster.
