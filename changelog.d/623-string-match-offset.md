- **Changed: `String#match` now accepts Ruby's optional offset.**
  `match(pattern, offset = 0)` searches for the first match starting at or after
  the given character (codepoint) position, so callers can scan from a known
  point without slicing the receiver first. A non-negative offset searches
  forward from that position; a negative offset counts back from the end (with an
  offset before the start returning `nil`); a positive offset greater than the
  receiver length is clamped to the length and the search runs from the end, so a
  zero-width-capable pattern matches the empty string there while a pattern that
  needs a character returns `nil`. The offset accepts an integer or a float (truncated
  toward zero, as in Ruby); any other type is rejected. Anchors such as `^`,
  `\b`, and `\B` keep the full-string context across the offset while `\A` only
  matches at the absolute start, and an invalid regex is still reported
  regardless of the offset.
