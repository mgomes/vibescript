- **Changed: `String#strip`, `String#lstrip`, and `String#rstrip` now match
  Ruby's whitespace set.** They remove only the ASCII whitespace bytes
  `\0 \t \n \v \f \r " "`, with NUL (`\0`) trimmed from both edges just like
  Ruby. Unicode spaces such as NBSP (`U+00A0`), the Ogham space mark (`U+1680`),
  em space (`U+2003`), and the byte order mark (`U+FEFF`) are now preserved
  instead of stripped. The bang variants still return `nil` when nothing is
  removed.
