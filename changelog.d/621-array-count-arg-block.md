- **Fixed: `Array#count(value)` ignores an attached block like Ruby.**
  `[1, 2, 1].count(1) { |x| x > 1 }` now returns `2` instead of raising. A value
  argument takes precedence: matching elements are counted and the block is
  never invoked. The block-only form `count { ... }` and the no-argument form
  `count` are unchanged.
