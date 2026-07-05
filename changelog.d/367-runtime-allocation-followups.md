- **Improved runtime allocation behavior for string splitting and array
  aggregation.** `String#split` now builds its result values directly instead
  of staging an intermediate `[]string`, and `Array#group_by`,
  `Array#group_by_stable`, and `Array#tally` avoid parallel key-value maps while
  preserving first-encounter result ordering.
