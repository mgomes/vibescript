- **Added: Ruby-style unary plus.** Prefix `+` is now a valid expression: it
  returns integers, floats, and strings unchanged and raises a clear runtime
  error on any other operand. Strings are immutable values, so `+"x"` yields the
  same string, matching Ruby's observable behavior. Like unary `-`, a sign
  written flush against its operand at the start of a line begins a new
  statement, while a sign with surrounding whitespace continues the previous
  line as binary addition.
