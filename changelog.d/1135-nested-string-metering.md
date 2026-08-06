- **Comparisons through arrays and hashes now charge for the string bytes they
  read.** The #1131 charge stopped at the operator boundary, so `[s] == [s]`,
  `{k: s} == {k: s}`, `[s] <=> [s]`, `include?`, `index`, `count`, `sort`,
  `max`, `uniq`, set operations, `eql?`, and `case/when` all scanned
  arbitrarily long payloads for a flat handful of steps — a loop over
  host-supplied strings with a long common prefix ran unbounded on a constant
  budget. The charge now lands at the recursive scalar comparison with the
  operator-level rules (equality bills only equal-length pairs, ordering
  bills the common prefix), shared and cyclic structures are billed once per
  distinct pair, and string-like hash and set keys are charged at every
  key-canonicalization site, closing the flat-cost `[s].uniq` and hash-key
  hashing gap noted alongside. `count(value)` and `hash.value?` also gain the
  per-element step their sibling scans already charged. Payloads under 64
  bytes round to free, so ordinary scripts see no new cost.
