- **Fixed docs: leftover pre-ADR-006 teaching is gone.** Collection `equal?`
  is content equality, including the exported `value.Value.Identical`
  contract, hashes are one string keyspace rather than symbol-keyed, the
  `function` type is not listed as live, and the 1.0 migration guide now
  covers Tasks/`sleep` and the string keyspace. `Hash.new(0)` is a runtime
  error, not a compile error. Hover on `Proc` teaches the callable removal.
