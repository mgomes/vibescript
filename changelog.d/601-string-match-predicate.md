- **Added: Ruby-style `String#match?`.** `match?(pattern, offset = 0)` is the
  allocation-light boolean counterpart to `match`, returning `true` when the
  pattern has a match at or after the given character offset and `false`
  otherwise without materializing match arrays. It shares the same regex engine
  and size guards as `match`, so anchors such as `\A`, `^`, and `\b` keep the
  full-string context even when an offset is supplied. The offset is a character
  (codepoint) position; an offset past the end of the string yields `false`
  rather than an error, and negative offsets are rejected to match `index` and
  `rindex`.
