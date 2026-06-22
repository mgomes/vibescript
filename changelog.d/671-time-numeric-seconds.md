- **Added: Ruby-style numeric-second `Time` arithmetic.** `time + number` and
  `time - number` now treat the number as seconds, matching Ruby. Integers shift
  by whole seconds, floats carry sub-second precision down to the nanosecond, and
  negative values shift backward. Numeric addition commutes (`number + time`),
  and an out-of-range or non-finite offset raises a runtime error.
- **Changed: `time - time` now returns a `Float` number of seconds** instead of a
  whole-second `Duration`, matching Ruby's `Time#-` and preserving sub-second
  precision.
