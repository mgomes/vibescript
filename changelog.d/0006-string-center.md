- **Added: Ruby-style string padding helpers.** `String#center`, `String#ljust`,
  and `String#rjust` pad a string to a requested width, defaulting to a single
  space and accepting a custom pad string that is repeated and truncated to fill
  the span. Width is measured in characters (Unicode code points) like
  Vibescript's other string methods, a `Float` width is truncated toward zero as
  Ruby does, a width at or below the receiver's length returns it unchanged, and
  an empty pad string is rejected. Oversized widths are checked against the
  memory quota before any buffer is allocated, so they fail fast instead of
  materializing a huge string.
