- **Added: Ruby-style `Math` module.** The new `Math` namespace exposes the
  constants `Math::PI` and `Math::E` (readable with `::` or `.`) and the pure
  numeric helpers `sqrt`, `cbrt`, `sin`, `cos`, `tan`, `asin`, `acos`, `atan`,
  `atan2`, `exp`, `log`, `log2`, `log10`, and `hypot`. Integer arguments are
  promoted to floats and every helper returns a `float`, matching Ruby.
  Arguments outside a function's domain (e.g. `Math.sqrt(-1)`, `Math.asin(2)`,
  or `Math.sin`'s well-defined relatives applied beyond their range) raise a
  domain error like Ruby's `Math::DomainError`. In-domain special values follow
  Ruby and IEEE 754: `Math.log(0)` returns `-Infinity`, `Math.sin`/`cos`/`tan`
  of `Infinity` return `NaN`, and a `NaN` argument propagates unchanged.
