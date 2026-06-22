- **Added: Ruby-style `String#hex` and `String#oct`.** `hex` reads a string as a
  hexadecimal integer and `oct` reads it using a base inferred from a
  `0x`/`0b`/`0o`/`0d` prefix (defaulting to octal). Both skip leading whitespace
  and an optional sign, accept underscore digit separators, stop at the first
  invalid digit, and return `0` for unparseable input, matching Ruby. Because
  Vibescript has only 64-bit integers rather than Ruby's `Bignum`, a value
  outside the `int64` range raises an `integer out of range` error. Overflow is
  now detected exactly before each digit is accumulated, so magnitudes that wrap
  past `uint64` (for example 17-or-more hexadecimal digits) raise the error
  instead of silently returning wrapped garbage.
