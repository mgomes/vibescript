- **Added: Ruby-style `Array#min`, `#max`, `#minmax`, `#min_by`, and `#max_by`.**
  The extrema helpers reuse the comparison semantics of `sort`/`sort_by`, return
  `nil` (or `[nil, nil]` for `minmax`) on empty arrays, resolve ties to the first
  matching element, participate in step/cancellation accounting for the block
  forms, and raise clear errors on incomparable mixed values.
