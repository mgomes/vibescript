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
   Measured below; the verdict is to keep eager validate-and-clone for
   now.

## Capability clone cost at large payloads (issue #129)

Benchmark run date: 2026-06-09 (master `9be1f7f`, Go 1.26.3, Apple M4).
The benchmarks live in
`internal/runtime/capability_clone_benchmark_test.go` and push row-set
payloads through the contracted `db` adapter in both directions:

- `BenchmarkCapabilityContractLargeReturn` — host to script: `db.query`
  returns an array of N row hashes (six mixed scalar fields each), which
  the boundary validates and deep-clones before the script sees it.
- `BenchmarkCapabilityContractLargeArgs` — script to host: the script
  passes a hash wrapping N row hashes to `db.update`, so the boundary
  validates the attributes argument and deep-clones it into the host
  request. The payload enters `Script.Call` as a host argument, so this
  direction also pays the call-entry rebind copy
  (`callFunctionRebinder.rebindValue`) before the capability boundary is
  reached.
- `BenchmarkCapabilityContractDeepNestedReturn` — `db.find` returns a
  hash chain nested 10,000 levels deep, exercising the recursive walks
  on depth instead of breadth.

Command:

```bash
go test ./internal/runtime/ -run '^$' \
  -bench '^BenchmarkCapabilityContract' -benchmem -count 3 -cpu 1
```

Medians of three runs:

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| LargeReturn/rows_100 | 122,872 | 110,928 | 407 |
| LargeReturn/rows_1000 | 1,299,533 | 1,390,928 | 2,321 |
| LargeReturn/rows_10000 | 13,309,549 | 11,890,672 | 20,964 |
| LargeArgs/rows_100 | 295,614 | 286,648 | 785 |
| LargeArgs/rows_1000 | 3,421,514 | 4,009,841 | 4,723 |
| LargeArgs/rows_10000 | 34,495,423 | 33,688,887 | 42,702 |
| DeepNestedReturn (depth 10,000) | 10,270,685 | 9,334,192 | 20,825 |

Cost scales linearly with payload size in both directions (each 10x size
step costs 10.1x-11.6x), roughly 1.3 us per row returned and 3.5 us per
row passed as an argument at the 10,000-row size. The depth case shows
the recursive validate and clone walks handle 10,000 levels of nesting
without issue, at about 1 us per level.

Profile of the largest case (`LargeArgs/rows_10000`, `benchtime 5s`,
captured with `scripts/bench_profile.sh` into a temporary directory;
profile artifacts intentionally not committed):

- CPU (7.55s of samples; about half is allocator/GC machinery —
  `runtime.madvise` 32.3% plus `runtime.kevent` 18.2% — driven by the
  overall allocation pressure):
  - memory estimator (issue #127): 26.8% cumulative
  - contract validation (`containsCallable` + `containsCycle` via
    `ValidateDataOnlyValue`): 8.1%
  - `DeepCloneValue` (the clone itself): 3.3%
  - contract scanners (`collectBuiltins` + `bindContracts`): 2.5%
- Allocations (8.26GB alloc_space):
  - memory estimator: 62.3% (`stringPayloadSize` alone is 46.9% flat)
  - call-entry rebind of host args (`rebindValue`): 14.6%
  - `DeepCloneValue`: 12.4%
  - validation seen-sets (`containsCallable` 3.55% +
    `containsCycle` 3.45%): 7.0%
  - contract scanners: 3.5%

Two findings worth recording:

1. Arguments are validated twice per contracted call: once by the
   pre-call contract enforcement in `invokeCallable`
   (`internal/runtime/call.go`), and again by the same validator
   re-invoked inside the adapter's `Call*` body
   (`vibes/capability/db/calls.go`). The pprof caller split confirms
   both call sites (~41% / ~59% of the validation time). That is
   deliberate defense in depth, but it means the data-only and cycle
   walks each run twice on every large payload; collapsing to a single
   enforcement point would save roughly 4% CPU and 3.5% of allocations
   in this workload.
2. In the script-to-host direction the dominant per-payload copies are
   the call-entry rebind (14.6% of allocations) and the memory
   estimator's traversal, not the capability clone.

Verdict: do not implement lazy or shallow validation now. The deep clone
itself is 12.4% of allocations and 3.3% of CPU in the worst case we
could construct — real but secondary. The same payloads cost five times
more in the memory estimator, so issue #127 dominates any boundary-side
optimization and would also change these ratios; re-measure after it
lands. If capability-heavy workloads still need wins afterwards, the
first candidate is eliminating the duplicated validation pass (a design
decision about keeping a single enforcement point, not a perf hack),
ahead of any laziness in the clone, which would weaken the isolation
guarantee the boundary exists to provide.
