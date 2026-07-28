- **Fixed: `Hash#map` ignored the block's arity and hid its retained output.**
  A block using numbered parameters (`{ _2 }`) received the whole `[key, value]`
  pair as `_1` and nil as `_2`; the yield shape now follows the block's
  positional arity as every other hash iterator does. The accumulating result is
  also reserved as releasable scratch, so a large temporary allocated inside a
  later block call is measured against a baseline that includes what the loop
  has already retained -- previously the two could pass separately though they
  coexisted.
