- **Added: Ruby-style `Array#reject`, `take_while`, `drop_while`, `grep`, and
  `grep_v`.** `reject` is the inverse of `select`; `take_while` and `drop_while`
  split on the first block miss with early-stop semantics; `grep` and `grep_v`
  filter using the language's case-equality direction (`pattern === element`,
  the same matcher as `case`/`when`), so a `Range` matches by membership and
  other values by equality, with an optional block transforming each kept
  element. Regex patterns are not yet available, so string patterns match by
  equality rather than substring.
