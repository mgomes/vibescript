- **Added: Ruby-style `String#casecmp` and `String#casecmp?`.** `casecmp`
  case-insensitively compares two strings (folding only ASCII letters and
  comparing other bytes ordinally) and returns `-1`, `0`, `1`, or `nil` for a
  non-string argument, matching Ruby. `casecmp?` returns a boolean using Unicode
  simple case folding (consistent with `upcase`/`downcase`) or `nil` for a
  non-string argument; full-fold expansions such as `ß` matching `SS` are not
  applied. When either operand contains invalid UTF-8, `casecmp?` folds
  byte-wise over ASCII letters so distinct byte sequences stay distinct,
  preserving byte identity like Ruby's binary-string path.
