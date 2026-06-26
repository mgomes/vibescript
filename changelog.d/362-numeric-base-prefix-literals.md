- **Added: Ruby-style numeric base prefix literals.** Integer literals now
  accept `0x`/`0X` hexadecimal, `0b`/`0B` binary, `0o`/`0O` octal, and `0d`/`0D`
  explicit decimal prefixes, with underscores permitted between digits in any
  base (`0xDEAD_BEEF`). A prefix must be followed by at least one valid digit,
  and a prefixed literal may not carry a fractional part or trailing letters;
  such malformed literals now fail at parse time with an `invalid numeric
  literal` diagnostic instead of leaving a stray identifier that produced a
  confusing undefined-variable error. A bare leading zero (`010`) remains
  decimal rather than legacy octal.
