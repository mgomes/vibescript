- **Added: Ruby-style numeric rounding precision.** `Float#round`, `Float#floor`,
  and `Float#ceil` now accept an optional Integer precision: positive `ndigits`
  keep the value a float rounded to that many fractional digits, while zero or
  negative `ndigits` return an integer bucketed to a power of ten. `Integer#round`,
  `Integer#floor`, and `Integer#ceil` gained the same precision argument, leaving
  the value unchanged for non-negative precision and bucketing for negative
  precision. Float rounding matches Ruby's half-away-from-zero correction, and
  any conversion back to an integer keeps the existing 64-bit overflow checks
  instead of widening like Ruby's bignums.
