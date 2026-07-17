- **Added: typed signatures in builtin metadata.** Builtin definitions can now
  declare positional, keyword, and result types; the checker validates known
  arguments and infers known results from the resolved builtin, while
  argument-dependent contracts and host overrides stay unknown.
