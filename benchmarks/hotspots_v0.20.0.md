# v0.20.0 Hotspot Priorities

Profile run date: 2026-02-21

Command:

```bash
scripts/bench_profile.sh \
  --pattern '^(BenchmarkCallShortScript|BenchmarkExecutionCapabilityFindLoop|BenchmarkExecutionArrayPipeline)$' \
  --benchtime 1s
```

CPU profile source: `benchmarks/profiles/v0.20.0-hotspots/cpu.top.txt`

## Top 3 CPU Paths (by cumulative time)

1. `(*Execution).estimateMemoryUsage` (~49.77% cumulative)
2. `(*memoryEstimator).env` (~46.03% cumulative)
3. `(*memoryEstimator).value` (~35.75% cumulative)

## Supporting allocation signals

From `benchmarks/profiles/v0.20.0-hotspots/mem.top.txt`:

- `newExecutionForCall` is the largest allocator (~35.55% alloc space).
- `newEnvWithCapacity` is the second largest allocator (~24.87% alloc space).
- `(*Env).Define` remains a top allocator (~6.96% alloc space).

## Priority order for next optimization passes

1. Cut memory-estimation traversal cost (`estimateMemoryUsage` + estimator walkers).
2. Reduce env/map churn in call setup (`newExecutionForCall`, `newEnvWithCapacity`, `Env.Define`).
3. Re-check capability-path call overhead after call-setup optimizations.
