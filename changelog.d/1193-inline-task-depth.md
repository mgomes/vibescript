- **Fixed: nested task groups can no longer grow the host stack without bound.**
  A group the shared concurrency pool cannot staff runs its job inline, on the
  goroutine already waiting for it; that is what stops a starved group
  deadlocking. But the inline call builds a whole new `Execution`, and the
  recursion limit counts frames within one `Execution`, so nothing counted the
  levels. A task function that opened another starved group per level therefore
  recursed across task boundaries forever, each level carrying a fresh recursion
  cap, step quota and memory quota. Against a host allowing one worker each
  level cost 20 Go frames and one nested `Execution`, dead linear to 5,000
  levels — 100,018 frames and 64 MiB of goroutine stack — with no error raised;
  Go's 1 GB stack limit, which kills the process rather than the script, sits
  around 76,000. Task jobs now nest at most sixteen deep on one goroutine. A job
  run inline also continues its caller's remaining recursion budget instead of
  taking a fresh one, so the recursion limit means something across a task
  boundary. Nothing changes for a job that gets a worker of its own: its
  goroutine's stack starts empty, so it keeps the host's whole limit.
- **Changed: very deeply nested task groups now report a limit instead of
  running serially.** This affects one shape: the concurrency pool is fully
  spent *and* the job would have run on its caller's stack, sixteen levels deep.
  Nesting is unaffected while any worker is free — a chain of groups that each
  get one runs to seventy-odd levels on the defaults, bounded by the pool as
  before. The shape that can reach the new limit is recursive
  divide-and-conquer, which nests one level per split: on the defaults that is
  fine past 65,536 leaves, further than a step quota lets a script get, but a
  split that goes deeper than sixteen levels below the point the pool ran dry
  now fails where it used to run serially. It fails loudly — `task nesting
  exceeds the inline depth limit 16`, raised through the task handle like any
  other task failure — rather than deadlocking or returning a wrong answer, so
  it is a limit to raise or a recursion to give a cutoff, not a silent change.
  The bound is a multiplier on memory as much as on stack: every level is a live
  `Execution` holding its own `MemoryQuotaBytes` at the same time as all the
  others, so the sandbox's total allowance is the worker pool times this depth
  times that quota.
