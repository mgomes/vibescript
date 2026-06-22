- **Added: Ruby-style `String#chop` and `String#chop!`.** `chop` removes the
  last character, treating a trailing `"\r\n"` as a single record separator and
  otherwise removing one full Unicode character rather than one byte; an empty
  string is returned unchanged. `chop!` returns the chopped string and returns
  `nil` when there is nothing to remove (the empty-string case), matching the
  existing copy-on-transform bang helper convention.
