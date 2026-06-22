- **Hardened CLI source-size enforcement.** `vibes run`, `vibes analyze`, and
  `vibes test` now read each script through a single size-checked descriptor,
  bounded at the engine's configured source-size limit, so an oversized file
  (even one swapped or grown after the check) is rejected before it is loaded
  fully into memory.
