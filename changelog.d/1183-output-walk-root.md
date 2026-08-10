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
  re-walks that forces are charged to the step quota rather than taken for free.
  That charge is settled as the lookup returns, so a callback that mutates state
  and then raises pays for the walks it forced exactly as one that returns does,
  and a rescued failure leaves nothing behind for a later lookup to be billed for.
  The graph walks such a callback forces are billed too, so the accounting cannot
  be bought cheaply by invalidating the memo. `VIBES_ESTIMATOR_VERIFY` re-derives
  every commit from scratch.
- **Changed: a lookup whose callback destructures with a named rest costs more
  steps.** Such a callback is the only one that makes `Hash#fetch_values` or
  `Hash#values_at` weigh a binding against the reachable graph, and those graph
  walks previously reached no counter: a loop of ten such lookups over a
  20,000-element graph cost the same 231 steps as over a 2,000-element one. They
  are now billed at the same rate as every other estimator walk in the tree, so
  the cost scales with the graph the callback is weighed against -- that loop now
  costs about 15,900 steps. A script doing this in bulk under a tight `StepQuota`
  may need a larger one. Callbacks without a named rest are unaffected.
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
