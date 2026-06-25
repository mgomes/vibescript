- **Added: Ruby-style anonymous rest targets in destructuring assignment.** A
  bare `*` may now appear as a rest target, discarding the values it captures
  without binding a name, as in `first, * = values`, `*, last = values`, and
  `first, *, last = values`. This matches Ruby's `*` discard target and joins
  the existing named `*rest` support.
- **Fixed: rest destructuring no longer panics on short right-hand sides.**
  Assignments such as `first, *rest = []` or `first, *, last = [1]` now bind the
  rest target to an empty array (and missing fixed targets to `nil`) instead of
  crashing on an out-of-range slice.
- **Fixed: trailing targets after a rest now bind left-to-right on short
  inputs.** When the right-hand side is shorter than the fixed targets, the
  targets after the rest fill from left to right and pad with `nil` on the
  right, matching Ruby. For example, `a, *, y, z = [1, 2]` now yields `a = 1`,
  `y = 2`, `z = nil` instead of reversing the trailing values.
