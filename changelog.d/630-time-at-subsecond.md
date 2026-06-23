- **Added: Ruby-style subsecond arguments for `Time.at`.** `Time.at` now accepts
  an optional second positional subsecond value and an optional third positional
  unit symbol in addition to int/float epoch seconds. The subsecond value
  defaults to microseconds, and the unit may be `:microsecond`/`:usec`,
  `:millisecond`, or `:nanosecond`/`:nsec` (e.g. `Time.at(0, 123456)` and
  `Time.at(0, 123456789, :nsec)`). The `in:` zone keyword composes with every
  form. A unit symbol without a subsecond value, an unknown unit, or a
  non-numeric subsecond value raises a runtime error. Unlike the calendar
  constructors (`Time.utc`/`Time.local`), `Time.at` does not treat an explicit
  `nil` subsecond as omitted: `Time.at(0, nil)` raises just as Ruby does.
  Subsecond values truncate
  toward zero at nanosecond resolution rather than retaining Ruby's
  arbitrary-precision rationals. A subsecond magnitude too large to express
  within that nanosecond range is rejected with `Time.at subsecond value out of
  range` instead of silently wrapping into a bogus instant.
