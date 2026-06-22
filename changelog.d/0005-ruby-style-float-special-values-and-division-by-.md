- **Added: Ruby-style float special values and division-by-zero behavior.** Float
  division by zero with the `/` operator (and `Float#fdiv`/`Integer#fdiv`) now
  follows IEEE 754 like Ruby instead of raising: a finite nonzero numerator
  yields `Infinity` or `-Infinity` and a zero numerator yields `NaN`. Integer
  division by zero (`1 / 0`) still raises, matching Ruby's `ZeroDivisionError`.
  Floats gained `nan?` (true only for `NaN`), `infinite?` (`1` for `Infinity`,
  `-1` for `-Infinity`, `nil` otherwise), and `finite?` (true when neither
  infinite nor `NaN`). Special values print as `Infinity`, `-Infinity`, and
  `NaN`, and `JSON.stringify` continues to reject non-finite floats because JSON
  has no representation for them. `div`, `divmod`, `modulo`, and `remainder` keep
  raising on a zero divisor, matching Ruby. Comparisons follow IEEE 754:
  comparisons against `NaN` are unordered, so `<`, `<=`, `>`, and `>=` return
  `false` and the spaceship operator `<=>` returns `nil`. Coercing a non-finite
  float to an integer now raises rather than silently yielding a garbage value,
  so a `NaN`/`Infinity` range endpoint, `money_cents` amount, or duration operand
  reports a clear error.
