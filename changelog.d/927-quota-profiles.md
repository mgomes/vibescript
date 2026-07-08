- **Added: an explicit `Unlimited` quota spelling and named quota profiles.**
  `Config` now distinguishes an unset quota (zero, which selects the built-in
  default) from an explicitly unbounded one (`Unlimited`), so a host can lift a
  step, memory, or recursion ceiling instead of only tightening it. The named
  profiles `ProfileLow`, `ProfileMedium`, `ProfileHigh`, and `ProfileXHigh`
  bundle the three quotas into a single coherent budget; `xhigh` runs a script
  like a normal interpreter (unlimited steps and memory) while keeping a finite
  recursion cap so runaway recursion still fails with a clean error.
