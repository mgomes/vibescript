- **Fixed: `first(n)` works on endless ranges.** `(1..).first(3)` returns
  `[1, 2, 3]` instead of rejecting with `cannot iterate an endless range`: the
  leading window starts at the known endpoint, so it is bounded work despite
  the open end, and it charges the sandbox step and memory quotas like any
  materializer. Genuinely unbounded operations (`each`, `to_a`, `last` on the
  open side) still reject up front, and beginless ranges still reject `first`
  entirely, matching Ruby. A window reaching the integer ceiling stops at the
  largest representable integer rather than wrapping.
