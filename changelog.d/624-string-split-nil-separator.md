- **Changed: `String#split(nil)` now matches Ruby.** An explicit `nil`
  separator behaves like the no-argument form, splitting on runs of ASCII
  whitespace instead of raising a type error. Any other non-string separator
  still raises an error.
