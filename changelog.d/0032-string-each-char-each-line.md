- **Added: Ruby-style `String#each_char` and `String#each_line`.** `each_char`
  yields each Unicode character (whole code points, matching `chars`, `length`,
  and `slice`), and `each_line` yields each line with Ruby-compatible newline
  retention (matching `lines`: only `\n` ends a line, a trailing newline does not
  produce a final empty line, and `\r\n` keeps the `\r` attached). Both ignore
  the block's return value and return the receiver string. Vibescript has no
  `Enumerator`, so calling either without a block reports a deliberate
  `requires a block` error instead of falling through as an unknown method.
