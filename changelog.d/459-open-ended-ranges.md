- **Added: Ruby-style beginless and endless ranges.** `start..`, `start...`,
  `..finish`, and `...finish` are supported for slicing (`arr[1..]`, `s[..2]`,
  `values_at`, `fill`, `byteslice`), case/`===` membership (`when 3..`),
  `cover?`/`include?`, one-sided `clamp`, hash keys, and rendering. Every
  iterating helper (`each`, `map`, `to_a`, `size`, `step`, `for`, `min`/`max`,
  `first(n)`/`last(n)`, `rand`) rejects open ranges up front instead of running
  into the sandbox quotas.
- **Changed: a newline ends a range at statement level.** `x = 1..` is now an
  endless range and the next line parses as a separate statement, matching
  Ruby; bounded endpoints may still continue onto the next line inside parens,
  brackets, and call arguments.
