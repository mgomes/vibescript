- **Fixed: static check no longer takes exponential time on deeply nested
  conditionals.** `vibes run -check` and the `CheckWarnings*` API used to grow
  ~4x per two levels of nested `if`/`elsif` and hung near depth 300; deep
  nesting now checks in milliseconds. As a backstop, the checker also rejects
  control flow nested beyond 512 levels with a deterministic
  `check exceeded maximum nesting depth of 512` diagnostic instead of
  stalling. The cap applies only to check mode: the parser and runtime accept
  and execute such scripts unchanged.
