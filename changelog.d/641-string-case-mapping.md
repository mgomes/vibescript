- **Changed: `String#upcase`, `downcase`, `capitalize`, and `swapcase` now use
  full Unicode case mapping.** Characters that expand or use special mappings
  follow Ruby, so `"Straße".upcase` is `"STRASSE"`, `"İ".downcase` is `"i̇"`,
  `"ﬁ".upcase` is `"FI"`, and `"ǆ".capitalize` titlecases the digraph to
  `"ǅ"`. The Greek final-sigma rule is not applied, matching Ruby's default
  (`"ΟΔΟΣ".downcase` is `"οδοσ"`). Strings that are not valid UTF-8 fall back to
  ASCII-only mapping, mirroring Ruby's binary-string path.
- **Added: case-mapping options for the string case methods.** `upcase`,
  `downcase`, `capitalize`, and `swapcase` accept `:ascii` to restrict mapping
  to ASCII letters, and `downcase` additionally accepts `:fold` for Unicode case
  folding (so `"Straße".downcase(:fold)` is `"strasse"`). Supplying `:fold` to a
  method other than `downcase`, an unknown option symbol, a non-symbol argument,
  or more than one option raises a clear error. The bang variants accept the
  same options and continue to return `nil` when the value is unchanged. Swapcase
  of a titlecase digraph (such as `ǅ`) is lowercased rather than split into its
  component letters, a deliberate divergence from Ruby for those rare codepoints.
