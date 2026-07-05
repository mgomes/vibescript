- **Fixed: block bodies now charge an ephemeral receiver against the memory
  quota alongside what they retain.** Iterating a receiver held only by the
  builtin's call frame (for example `make_hash().each do |(k, (head, *tail))|`)
  with a rest-collecting destructure used to count the receiver only in the
  one-time bind-charge snapshot while the body's checks counted only the
  environment, so a loop copying tails into a retained accumulator could
  transiently hold roughly twice `MemoryQuotaBytes` without either view
  noticing. The bind charge now measures those Go-frame-only roots once and
  reserves them into the live baseline for each block call, so per-statement
  and mutator-growth checks bound the combined peak and reject it mid-loop at
  the quota. Receivers reachable from the environment deduplicate to a ~zero
  reservation, and the reservation itself is O(1) per call, so existing
  workloads are unaffected.
