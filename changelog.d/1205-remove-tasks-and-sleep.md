- **Removed: the `Tasks` namespace and script-visible `sleep`.** Per
  [ADR-006](docs/adr/006-slim-language-for-predictable-sandboxing.md), hosts own
  concurrency and delay. `Tasks.run`, `Tasks.map`, `tasks.spawn`, `tasks.wait`
  and `task.value` are gone, as is `sleep`. Scripts that fanned out with
  `Tasks.map(items, with: :work)` move that loop to the host, which runs
  independent `Script.Call` invocations concurrently — separate execution state
  per call, so the host's own goroutine pool, tracing, cancellation and
  rate limits apply — or exposes a bounded batch operation as a capability with
  its own aggregate limits. Scripts that used `sleep` to model a delayed
  workflow step move it to the host's timer or durable job system, which owns
  the step's lifecycle outside an interpreter call.
- **Removed: `Config.DefaultTaskConcurrency`, `Config.MaxTaskConcurrency` and
  `Config.MaxSleepDuration`.** Setting them is now a compile error rather than
  a silent no-op. `QuotaProfile.MaxSleepDuration` is gone with them, so a
  profile is a bundle of three quotas rather than four, and `ConfigSummary`
  reports `steps`, `memory` and `recursion` only. Step, memory and recursion
  remain the sandbox budgets and are unchanged.
- **Changed: the memory quota no longer spans a chain of nested calls.** The
  chain existed to stop nested task levels each receiving the host's whole
  allowance; with task nesting removed there is no such chain, so
  `MemoryQuotaBytes` is again one bound on one execution's reachable graph. A
  host that reached a callee on another engine through a context carrying a
  chain node no longer passes a ceiling to it; every other path is unchanged.
