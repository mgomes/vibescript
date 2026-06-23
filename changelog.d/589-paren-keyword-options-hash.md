- **Fixed: parenthesized function calls bind keyword labels to a positional
  options hash like the parenless form.** When a plain function has no matching
  keyword parameter and exposes a positional options parameter,
  `configure(retries: 3)` now collapses its keyword labels into the options hash
  just as `configure retries: 3` already did, and a typed options parameter is
  validated against the synthesized hash so `configure(retries: "slow")` is
  rejected with the shape mismatch instead of `missing argument`. Constructor
  and method calls keep strict parenthesized keyword binding.
