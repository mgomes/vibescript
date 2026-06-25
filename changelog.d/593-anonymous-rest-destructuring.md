- **Added: Ruby-style anonymous rest targets in destructuring assignment.** A
  bare `*` may now appear as a rest target, discarding the values it captures
  without binding a name, as in `first, * = values`, `*, last = values`, and
  `first, *, last = values`. This matches Ruby's `*` discard target and joins
  the existing named `*rest` support.
- **Fixed: anonymous rest targets now parse in block parameter
  destructuring.** A bare `*` discard rest is accepted inside destructured block
  parameters, as in `values.map do |(head, *)| ... end` and
  `do |(head, *, tail)| ... end`, matching assignment destructuring instead of
  being rejected as an invalid block parameter target.
- **Fixed: rest destructuring no longer panics on short right-hand sides.**
  Assignments such as `first, *rest = []` or `first, *, last = [1]` now bind the
  rest target to an empty array (and missing fixed targets to `nil`) instead of
  crashing on an out-of-range slice.
- **Fixed: trailing targets after a rest now bind left-to-right on short
  inputs.** When the right-hand side is shorter than the fixed targets, the
  targets after the rest fill from left to right and pad with `nil` on the
  right, matching Ruby. For example, `a, *, y, z = [1, 2]` now yields `a = 1`,
  `y = 2`, `z = nil` instead of reversing the trailing values.
- **Fixed: destructuring now snapshots the right-hand side before assigning.**
  When a target writes back into the source array, later targets read the
  original values rather than the mutated ones, matching Ruby's whole-RHS
  evaluation. For example, `values = [1, 2, 3]; values[1], *rest = values` now
  binds `rest` to `[2, 3]` (the original snapshot) instead of `[1, 3]`.
- **Fixed: a splat-assignment that begins a line after a continuable
  expression now parses as its own statement.** A line that opens with `*` and
  forms a destructuring left-hand side, such as `*, last = values` or
  `*rest, last = values`, is no longer misread as a multiplication continuation
  of the previous line. This holds even when the `=` lands on the next line via
  the newline-before-`=` continuation (for example `*rest` followed by an
  indented `= values`). Genuine multiline multiplication (a line ending or
  beginning with a spaced `*`) still continues as before. The lookahead also
  accepts reserved-word member names, so targets such as `*rest, record.end =
  values` start a new statement instead of failing to parse.
