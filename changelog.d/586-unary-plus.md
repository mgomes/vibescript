- **Added: Ruby-style unary plus.** Prefix `+` is now a valid expression: it
  returns integers, floats, and strings unchanged and raises a clear runtime
  error on any other operand. Strings are immutable values, so `+"x"` yields the
  same string, matching Ruby's observable behavior. A `+` (or `-`) written flush
  against its operand at the start of a line begins a new statement, matching
  Ruby. A sign separated from its operand by surrounding whitespace continues the
  previous line as a binary operator, reusing Vibescript's existing
  indented-continuation rule; this spaced form intentionally differs from Ruby,
  which would start a new statement instead.
