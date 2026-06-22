- **Added: `Time#round` precision argument.** `Time#round` now accepts an
  optional Ruby-style `ndigits` (defaulting to `0`) so `round(3)` and `round(6)`
  produce millisecond and microsecond precision, with non-negative `Integer`
  validation and clear errors on misuse.
