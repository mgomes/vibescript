- **Fixed: double-quoted strings work inside interpolation expressions.** An
  interpolation now extends to its matching `}` even when the embedded expression
  contains its own double-quoted strings or nested interpolations, so common
  fallback and helper shapes such as `"#{name || "guest"}"` and
  `"#{["a", "b"].join(", ")}"` parse instead of reporting an unterminated string
  interpolation, matching Ruby. The lexer no longer guesses where the outer
  string ends by scanning the rest of the input, so an unterminated string now
  reports a clear lexer error and a stray character reports `unexpected character`
  instead of a generic `invalid token`.
