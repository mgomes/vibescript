- **Added: Ruby-style `String#prepend` and `String#insert`.** `prepend` returns
  a copy of the receiver with one or more string arguments prepended in order,
  mirroring `concat`. `insert` returns a copy with a string inserted at a
  character index: a non-negative index inserts before the character at that
  position (a value equal to the length appends), while a negative index inserts
  after the character it selects (`-1` appends). The index counts characters
  rather than bytes, so it behaves the same way for multibyte text, a `Float`
  index is truncated toward zero as Ruby does, and an out-of-range index raises
  an error.
