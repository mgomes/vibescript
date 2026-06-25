- **Added: Ruby-style `for` iteration over hashes.** A `for` loop may now
  iterate a hash directly, mirroring Ruby's loop over `each`. Each iteration
  binds a two-element `[key, value]` pair (keys exposed as symbols), visited in
  sorted key order, and participates in the sandbox step and memory quotas like
  array and range iteration.
