- **Added: Ruby-style `String#chars` and `String#lines`.** `chars` returns an
  array of the string's Unicode characters using the existing rune-aware
  semantics, and `lines` splits on `"\n"` while retaining the trailing newline
  on each line, leaving carriage returns attached so `"\r\n"` endings round-trip.
