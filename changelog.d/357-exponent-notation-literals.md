- **Added: Ruby-style exponent (scientific) notation numeric literals.** Float
  literals may now carry an `e`/`E` exponent marker with an optional sign and
  one or more exponent digits (`1e3`, `1.5e-2`, `1E6`, `1e1_0`). Any literal
  with an exponent is a float even without a decimal point, so `1e3` is
  `1000.0`, matching Ruby. Underscores remain visual separators between exponent
  digits, and an exponent that overflows the 64-bit float range saturates to
  `Infinity` as in Ruby. A numeric literal that directly abuts an identifier
  (`1e3foo`, `123abc`, `1.5x`, `1e`, `1e_3`) now reports a clear parse error
  instead of silently splitting into a number followed by an identifier, and
  committed-but-malformed exponents (`1e+`, `1e3_`, `1e3__4`) are rejected the
  same way. Keyword suffixes stay valid so Ruby modifier forms like `5if cond`
  and `1e3if cond` continue to parse.
