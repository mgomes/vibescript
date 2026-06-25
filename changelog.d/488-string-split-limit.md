- **Added: Ruby-style `String#split` limit argument.** `split(separator, limit)`
  now accepts the optional second `limit` argument. A positive limit returns at
  most that many fields with the remainder left unsplit in the final field (a
  limit of `1` returns the whole string), and a negative limit preserves every
  field including trailing empties. The limit applies to every separator mode,
  including the whitespace default and the empty separator that splits a string
  into its characters. Splitting on the empty separator walks UTF-8 character
  boundaries, so invalid bytes in a binary string are preserved as single-byte
  fields rather than rewritten as the U+FFFD replacement character. A
  non-integer limit is rejected.
- **Changed: `String#split` now trims trailing empty fields by default.** With
  the default limit of `0`, `"a,b,".split(",")` returns `["a", "b"]` instead of
  `["a", "b", ""]`, matching Ruby. Use a negative limit to keep trailing empty
  fields.
- **Changed: a single space separator triggers whitespace splitting.** A
  separator of exactly `" "` is Ruby's AWK whitespace mode, so it collapses
  whitespace runs and discards leading whitespace instead of splitting literally.
  `" a  b ".split(" ", 2)` returns `["a", "b "]` rather than `["", "a  b "]`.
