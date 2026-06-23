- **Fixed: bounded result rendering for large composite return values.**
  `Value.String` now streams array and hash rendering directly into one growing
  buffer instead of building a `[]string` per element and a `fmt.Sprintf` per
  hash entry before joining, so formatting no longer allocates O(n) intermediate
  strings. The new `Value.StringBounded(limit)` renders a value while stopping
  at a byte budget and reporting `ErrStringRenderTruncated`, and the `vibes run`
  CLI uses it with a 1 MiB cap so a script that returns a huge nested array or
  hash fails with `result rendering exceeds …` instead of allocating the whole
  formatted string in host memory after the runtime quotas have already
  released. Cycle detection is unchanged.
