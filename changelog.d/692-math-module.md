- **Added: Ruby-style `Math` module.** The new `Math` namespace exposes the
  constants `Math::PI` and `Math::E` (readable with `::` or `.`) and the pure
  numeric helpers `sqrt`, `cbrt`, `sin`, `cos`, `tan`, `asin`, `acos`, `atan`,
  `atan2`, `exp`, `log`, `log2`, `log10`, and `hypot`. Integer arguments are
  promoted to floats and every helper returns a `float`, matching Ruby.
  Arguments outside a function's domain (e.g. `Math.sqrt(-1)` or `Math.asin(2)`)
  raise a domain error like Ruby's `Math::DomainError`, while `Math.log(0)`
  returns `-Infinity` and `NaN`/`Infinity` arguments propagate unchanged,
  following Ruby and IEEE 754.
