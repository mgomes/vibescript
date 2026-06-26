- **Added: Ruby-style exponent (scientific) notation numeric literals.** Float
  literals may now carry an `e`/`E` exponent marker with an optional sign and
  one or more exponent digits (`1e3`, `1.5e-2`, `1E6`, `1e1_0`). Any literal
  with an exponent is a float even without a decimal point, so `1e3` is
  `1000.0`, matching Ruby. Underscores remain visual separators between exponent
  digits, and an exponent that overflows the 64-bit float range saturates to
  `Infinity` as in Ruby. Partial forms such as `1e`, `1e+`, and `1e_3` now
  report a clear parse error instead of silently splitting into an integer
  followed by an identifier.
