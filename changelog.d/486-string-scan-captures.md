- **Changed: `String#scan` returns Ruby-compatible capture results.** When the
  pattern has no capture groups `scan` still returns the full match strings, but
  with one or more groups it now returns a nested array per match holding each
  captured substring (`nil` for an optional group that did not participate),
  matching Ruby instead of always returning the full matches.
