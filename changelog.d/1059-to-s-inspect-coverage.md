- **Added: the missing `to_s` and `inspect` members.** Every value kind already
  rendered correctly through interpolation and inside a container's `inspect`,
  but the direct methods were absent on most kinds, and which kind had which
  corresponded to nothing -- `duration`, `money`, and `time` had `to_s` but not
  `inspect`, `array` had `inspect` but not `to_s`, `range` had neither. Array and
  range now render with `to_s`, and duration, money, and time with `inspect`. The
  aggregate renderings charge the memory quota exactly as `inspect` does.
