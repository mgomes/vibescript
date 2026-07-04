- **Added: multiple ordered `rescue` clauses.** A `begin` block (and a
  function-level rescue tail) may now carry several `rescue` clauses, each with
  its own error type and `=> binding`. The first clause whose type matches the
  raised error handles it, so handlers order from specific to general exactly
  as in Ruby. Previously only a single clause parsed, forcing handlers to
  collapse error types into one union and losing ordered fallback behavior.
