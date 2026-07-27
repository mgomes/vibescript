- **Added: `vibes check` reports a space before a parenthesised argument.**
  `f (x).length` binds as `(f(x)).length` here but as `f((x).length)` in Ruby.
  Both readings produce a value and neither reported anything, so the difference
  was invisible -- most damagingly in `puts (x).inspect`, where the one line
  written to see a value accurately renders it inaccurately. The binding is
  unchanged; only the ambiguity is now reported.
