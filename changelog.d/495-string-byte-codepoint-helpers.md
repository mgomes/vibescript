- **Added: Ruby-style `String` byte and code-point helpers.** `getbyte(index)`
  returns the byte at a byte offset (or `nil` when out of range), `byteslice`
  extracts a substring by byte offset (single index, `start`/`length`, or range
  forms, returning raw bytes verbatim like Ruby), `codepoints` returns the
  string's Unicode code points as an integer array, and `each_codepoint` yields
  each code point to a block. Negative offsets count back from the end, the
  byte-array helpers honor the sandbox memory quota, and the streaming
  `each_codepoint` participates in block cancellation and quotas. This
  complements the existing `bytes` and `each_byte`.
