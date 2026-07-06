- **Fixed: large blockless materializations no longer re-walk the whole heap
  every 16 steps.** Blockless `Array#flatten`, `chunk(n)`, `window`, `join`,
  and `reverse`, plus `Hash#to_a` and `Hash#flatten`, already charge every
  allocation against the memory quota before performing it, yet their
  per-element step accounting also re-ran the full reachable-graph memory walk
  each period, making big builds quadratic — a 1M-leaf `flatten` under raised
  quotas took minutes of CPU for a sub-second build. These loops now run as
  accumulator-metered sections that skip the redundant periodic walk while
  keeping the step-quota and context-cancellation checks on the same schedule.
  Quota acceptance thresholds are unchanged, and any script re-entry or nested
  builtin dispatch suspends the section so full checks always apply outside
  the metered loop.
