- **Fixed: `Array#first` and `Array#last` reject extra arguments like Ruby.**
  `[1, 2, 3].first(1, 2)` and `[1, 2, 3].last(1, 2)` now raise instead of
  silently ignoring the extra argument and returning `[1]` or `[3]`. The
  optional count is still the only accepted argument, so the no-argument forms
  and the single-count forms (including `first(0)` / `last(0)`) are unchanged.
