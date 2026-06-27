- **Added: Ruby-style block forms for `String#sub`, `String#gsub`, `String#scan`,
  and `String#match`.** `sub`/`gsub` (and their `!` variants) now accept a block
  instead of a replacement argument: the block receives each matched substring
  and its result (coerced to a string) replaces the match, honoring the same
  `regex` keyword as the value-replacement forms (defaulting to literal
  matching). `scan` with a block yields each match using its array result shape
  and returns the receiver string. `match` with a block yields the match data and
  returns the block's result, returning `nil` without invoking the block when
  there is no match. Supplying both a replacement argument and a block is
  rejected, and the block forms enforce the same output-size and step guards as
  the existing regex helpers.
