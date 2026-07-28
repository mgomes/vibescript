- **Fixed: `rescue ArgumentError` catches an incomparable comparison.** The
  language reference documents `<`, `<=`, `>`, and `>=` as raising Ruby's
  `ArgumentError` on operands that cannot be ordered, but `1 < "a"` raised an
  untyped error that only a bare `rescue` could catch, so a handler written from
  the documentation silently missed it. `<=>` is unaffected and still answers
  `nil`.
