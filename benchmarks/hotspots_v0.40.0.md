# v0.40.0 Hotspot Priorities

Profile run date: 2026-06-09 (master `3c99aec`, Go 1.26.3, Apple M4)

Command:

```bash
scripts/bench_profile.sh \
  --pattern '^(BenchmarkCallShortScript|BenchmarkExecutionCapabilityFindLoop|BenchmarkExecutionArrayPipeline|BenchmarkExecutionMethodDispatchLoop)$' \
  --benchtime 1s \
  --outdir benchmarks/profiles/v0.40.0-hotspots
```

The benchmark set matches the v0.20.0 run plus
`BenchmarkExecutionMethodDispatchLoop`, since it is an acceptance benchmark
for the call-setup work (issue #128). Raw profiles and `pprof` top summaries
live in `benchmarks/profiles/v0.40.0-hotspots/`.

## Top CPU paths (by cumulative time)

From `cpu.top.txt`:

1. `(*Execution).estimateMemoryUsageBase` (~54.6% cumulative)
2. `(*memoryEstimator).env` (~50.3% cumulative)
3. `(*memoryEstimator).value` (~24.6% cumulative) plus
   `(*memoryEstimator).slice` (~16.5% cumulative)

Generic map iteration (`internal/runtime/maps.(*Iter).Next`, ~17.8%
cumulative) is almost entirely the estimator walking env and hash maps, so
it folds into the same hotspot. `runtime.kevent`/`runtime.madvise` samples
are scheduler and GC noise on the bench host and are excluded from
prioritization.

## Supporting allocation signals

From `mem.top.txt` (alloc_space):

- `cloneBuiltinMapForCall` is now the largest single allocator (~26.4%);
  its caller `(*Engine).defineBuiltinsForCall` accounts for ~36.5%
  cumulative.
- `newEnvWithCapacity` (~21.6%) and `newExecutionForCall` (~18.3%) remain
  top allocators.
- `(*Env).DefineStatic` (~10.0%) and `(*Env).Define` (~5.6%) round out the
  call-setup churn.
- Capability-contract cloning: `CloneMethodResult` accounts for
  47.51MB cumulative (including the `DeepCloneValue` walk it performs)
  against `BenchmarkExecutionCapabilityFindLoop`'s 184.04MB total —
  roughly a quarter of that benchmark's allocations, though only ~2.8%
  of the combined four-benchmark profile. Issue #129 tracks measuring
  large payloads before deciding on lazy or shallow validation.

## Changes since v0.20.0

- Memory estimation is still the dominant CPU cost; the entry point moved
  to `estimateMemoryUsageBase` with the task-result quota accounting, but
  the shape (full env/value traversal) is unchanged. Issue #127 remains
  priority one.
- Per-call builtin cloning (`cloneBuiltinMapForCall` via
  `defineBuiltinsForCall`) has overtaken `newExecutionForCall` as the
  largest allocation source — a new signal that was not in the v0.20.0 top
  set and belongs to the issue #128 cluster.
- `newExecutionForCall` dropped from ~35.6% to ~18.3% of alloc space;
  `newEnvWithCapacity` is roughly unchanged (~24.9% to ~21.6%).

## Priority order for next optimization passes

1. Cut memory-estimation traversal cost (`estimateMemoryUsageBase` and the
   estimator walkers) — issue #127.
2. Reduce per-call builtin cloning and env churn
   (`cloneBuiltinMapForCall`, `defineBuiltinsForCall`,
   `newEnvWithCapacity`, `newExecutionForCall`, `Env.Define*`) —
   issue #128.
3. Benchmark capability-contract clone overhead with large payloads, then
   decide whether lazy or shallow validation is justified — issue #129.
