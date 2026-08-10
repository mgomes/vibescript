- **Fixed: a lookup's accumulated results are now visible to the memory quota
  while its callback runs.** `Hash#fetch_values` and `Hash#values_at` build their
  result in a Go local the estimator had no root for, so every check performed
  inside the block or default proc measured a graph missing everything the loop
  had already retained: a callback returning an individually permitted value
  could accumulate past `MemoryQuotaBytes` one accepted result at a time. Those
  outputs are now registered as base-walk roots, so each check re-derives what
  the output holds at the moment it runs and deduplicates it against the receiver
  and the arguments. The root covers the results produced so far rather than the
  slice sized from the argument count, so a wide lookup does not pay for its
  whole output on its first miss. Costs are unchanged while the callback leaves
  the base-walk memo intact; a callback that mutates state discards it, and the
  re-walks that forces are described below. Whatever a lookup is charged is settled
  as it returns, so a callback that mutates state and then raises pays what one
  that returns pays, and a rescued failure leaves nothing behind for a later lookup
  to be billed for.
  `VIBES_ESTIMATOR_VERIFY` re-derives every commit from scratch.
- **Changed: a lookup whose callback destructures with a named rest costs more
  steps.** Such a callback is the only one that makes `Hash#fetch_values` or
  `Hash#values_at` weigh a binding against the reachable graph, and that weighing
  builds a charge whose construction walk previously reached no counter. It is now
  billed, so the cost scales with the graph the callback is weighed against: four
  such lookups over a tenfold graph cost about 2.8 times the steps. A script doing
  this in bulk under a tight `StepQuota` may need a larger one. Callbacks without
  a named rest are unaffected.
- **Known: re-walking a lookup's retained results is not charged to the step
  quota, and a callback that mutates makes those re-walks quadratic.** When a
  callback mutates anything, the estimator's memo is discarded and the lookup's
  retained results are walked again on the next memory check, so the walking grows
  with the square of the number of results. Registering the output is what
  introduces that shape: the same script is linear without it. Over 200, 400 and
  800 missing keys with a counter-incrementing default proc, estimator work goes
  from 31,840 / 63,615 / 127,165 nodes to 36,275 / 112,475 / 384,875.

  That walking is deliberately not charged to the step quota. It is triggered by a
  memo whose key is process-wide, so an unrelated script running at the same time
  invalidates it just as a script's own mutation does, and billing the walk let one
  script's mutations push an unrelated script over its `StepQuota` -- a worse
  failure than leaving the walk uncharged.

  It is bounded, but by the quotas rather than by a small constant: the results
  walked are bounded by `MemoryQuotaBytes` and the number of walks by `StepQuota`,
  so the work cannot exceed their product. What that costs in practice depends on
  the shape. A callback that destructures with a named rest stays near 0.15 walks
  per step. A mutating callback over a wide lookup does not: estimator work per
  step rose from 16 to 44 across the three sizes above and keeps rising with the
  number of results retained, against a flat 14 without the output registered. A
  script doing this in bulk is doing more estimator work than its step count
  suggests. Charging it accurately needs per-execution mutation tracking, which is
  left to its own change.
- **Known: one nested lookup shape is charged more memory than it uses.** A
  `Hash#fetch_values` or `Hash#values_at` whose callback destructures with a named
  rest (`|(head, *tail)|`), nested inside another iterator's block, and returning a
  value that the enclosing block's own scope holds, is charged for that value
  twice from its second callback onward. A script doing this needs roughly one
  extra copy of the returned value's size in `MemoryQuotaBytes`. Nothing is let
  through unchecked -- the lookup is over-charged, not under-charged -- and the
  quota still bounds what it can allocate. Flat lookups, callbacks without a named
  rest, and nested lookups returning a value held outside the enclosing block are
  all unaffected and cost what they did before. This is a known cost of bounding a
  path that was previously unbounded, and the accounting fix is deliberately left
  to its own change.
