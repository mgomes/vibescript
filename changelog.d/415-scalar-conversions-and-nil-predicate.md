- **Added: Ruby-style scalar conversion methods and the `nil?` predicate on
  core values.** Every core value kind (`nil`, booleans, integers, floats,
  strings, symbols, arrays, hashes, ranges, money, durations, and times) now
  answers `nil?` (true only for `nil`), `to_s`, and the documented `.string`
  conversion idiom from `docs/typing.md`. Integers and floats convert between
  numeric kinds with `to_i`/`to_f` (`Float#to_i` truncates toward zero like
  Ruby and raises on a non-finite or out-of-range value). Strings parse numeric
  text with `to_i`/`to_f`; unlike Ruby's lenient `String#to_i`/`String#to_f`,
  these are strict like the global `to_int`/`to_float` and raise on an empty,
  non-numeric, or non-finite string so a malformed value never silently becomes
  zero at a typed boundary.
