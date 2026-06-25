- **Changed: `String#scan` returns Ruby-compatible capture results.** When the
  pattern has no capture groups `scan` still returns the full match strings, but
  with one or more groups it now returns a nested array per match holding each
  captured substring (`nil` for an optional group that did not participate),
  matching Ruby instead of always returning the full matches. `scan` charges its
  growing result against the step and memory quotas and bounds the regex engine's
  submatch-index allocation against a fixed 256 MiB host cap, so a pattern with
  many capture groups over a large subject errors instead of exhausting host
  memory. The host cap is derived from the subject length and the pattern's
  minimum match length, so ordinary sparse scans — a pattern that matches little
  or nothing over a modest string — run regardless of the configured memory quota
  instead of being rejected on a pessimistic worst case.
