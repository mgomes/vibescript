- **Added: Ruby-style `String#bytes` and `String#each_byte`.** `bytes` returns an
  array of the string's bytes as integers in `0..255`, and `each_byte` streams
  each byte to a block and returns the receiver. Both are byte-level, so a
  multibyte character expands to one entry per UTF-8 byte, and raw bytes are
  returned verbatim without normalizing invalid UTF-8. As with `each_char`,
  `each_byte` requires a block because Vibescript has no `Enumerator`.
