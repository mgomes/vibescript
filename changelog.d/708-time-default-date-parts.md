- **Added: Ruby-style default date parts for `Time` calendar constructors.**
  `Time.new`, `Time.utc`, `Time.gm`, `Time.local`, and `Time.mktime` now require
  only a year. As in Ruby, an omitted month or day defaults to `1` and omitted
  time fields default to midnight, so forms such as `Time.new(2024)`,
  `Time.utc(2024)`, and `Time.utc(2024, 2)` build January 1 (or the first of the
  given month) at the start of the day instead of raising an arity error. An
  explicit `nil` in an optional position is treated the same as omitting it, so
  `Time.utc(2024, nil)` builds January 1 rather than normalizing month `0` into
  the prior year.
