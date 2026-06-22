- **Added: Ruby-style range query and conversion helpers.** Ranges now answer
  `cover?`, `include?`, and `member?` membership predicates (numeric arguments
  are tested against the range bounds, exclusivity, and direction; any other
  type is never a member and returns `false` rather than raising), the metadata
  helpers `first`, `last`, `size`, and `exclude_end?`, and the `to_a`
  materialization. Because Vibescript iterates descending ranges such as `5..1`,
  `size`, `to_a`, `first(n)`, and `last(n)` report that descending sequence
  rather than the empty result Ruby produces; the remaining helpers match Ruby.
  `to_a` and the counted `first(n)`/`last(n)` forms build their arrays under the
  sandbox step and memory quotas so large ranges fail safely instead of
  exhausting memory.
