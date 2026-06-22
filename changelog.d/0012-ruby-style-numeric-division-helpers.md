- **Added: Ruby-style numeric division helpers.** Integers and floats now expose
  `div` (floored division returning an integer), `divmod` (the floored quotient
  paired with the divisor-signed modulo), `fdiv` (floating division), and
  `remainder` (truncated-division remainder whose sign follows the receiver, so
  it differs from `%` for mixed-sign operands). A zero divisor errors for all
  four, and quotients outside the 64-bit range error rather than wrapping. Ruby's
  `fdiv` infinity result is intentionally an error instead, matching the `/`
  operator, and `quo` is intentionally omitted because Vibescript has no rational
  number type.
