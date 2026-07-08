- **Added: a deep-recursion call-path benchmark and CI smoke gate.**
  `BenchmarkExecutionRecursiveFib` exercises naive recursive `fib`, which grows
  the environment stack with call depth — a shape none of the existing
  loop-based benchmarks covered — so call-setup allocation regressions are now
  caught in CI. A paired `BenchmarkExecutionRecursiveFibQuota` measures the same
  recursion under a memory quota for profiling the estimator path.
