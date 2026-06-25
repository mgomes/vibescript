- **Added: Ruby-style `Array#at` and `Array#slice`.** `at(index)` returns the
  single element at `index`, counting a negative index back from the end and
  returning `nil` out of range, so `[10, 20, 30].at(-1)` is `30`. `slice(index)`
  mirrors `at`, while `slice(start, length)` and `slice(range)` return a fresh
  subarray with Ruby-compatible handling of negative starts and bounds, a start
  exactly at the length (yielding `[]`), oversized lengths (clamped to the
  remaining elements), and negative lengths or out-of-range starts (returning
  `nil`). The range form aligns with the range slicing already available for
  strings. Indexes and lengths accept `Float` values truncated toward zero like
  Ruby's `to_int`, and the subarray forms never alias the receiver.
