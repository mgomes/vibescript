- **Added: Ruby-style subsecond parts for `Time.local`, `mktime`, `utc`, and
  `gm`.** These calendar constructors now read their seventh positional argument
  as microseconds-with-fraction instead of routing it through timezone parsing.
  Integer microseconds are exact and floats carry sub-microsecond precision down
  to the nanosecond, while a non-numeric microsecond argument raises a runtime
  error. `Time.new` keeps its Ruby distinction of accepting a zone/offset in the
  seventh position. Unlike Ruby, a string microsecond argument is rejected rather
  than coerced via leading-digit parsing.
