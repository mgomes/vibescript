- **Changed: default `String#split` now uses Ruby's ASCII whitespace set.**
  The no-separator form previously delegated to Go's `strings.Fields`, which
  treats wider Unicode whitespace such as the non-breaking space (`U+00A0`) and
  the em space (`U+2003`) as separators. It now splits only on the six ASCII
  whitespace bytes Ruby recognizes (space, tab, newline, vertical tab, form
  feed, and carriage return), keeping other Unicode whitespace inside the field
  so `"a b".split` returns `["a b"]` instead of `["a", "b"]`. Leading
  and trailing whitespace is still discarded and runs collapse, matching Ruby's
  default `String#split`.
