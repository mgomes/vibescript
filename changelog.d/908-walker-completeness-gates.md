- **Fixed: check-mode false positives from incomplete AST walkers.** `vibes
  run -check` now resolves locals bound inside destructuring index selectors
  and no longer flags later literal elements when a block body contains
  `retry`, which ends the iteration early just like `break`.
- **Improved: `vibes analyze` unreachable-statement coverage.** Statements
  following an unconditional `break`, `next`, or `retry` are now reported as
  unreachable, and nested definition bodies are linted.
- **Internal: AST walker completeness gates.** New tests enumerate every AST
  node type from source and fail when a hand-maintained walker (cloner,
  checker collectors, escape analysis, symbol interning, evaluator dispatch,
  analyzer) misses a type, and a reflection gate proves the AST cloner copies
  every field of every node without sharing state.
