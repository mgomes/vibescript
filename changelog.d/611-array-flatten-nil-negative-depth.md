- **Fixed: `Array#flatten` accepts `nil` and negative depths like Ruby.**
  `[1, [2, [3]]].flatten(nil)` and `[1, [2, [3]]].flatten(-1)` now flatten fully
  instead of raising, matching the no-argument form. A depth of `0` still returns
  a shallow copy, positive integers flatten that many levels, a `Float` depth is
  truncated to an integer, and a nonnumeric depth raises.
