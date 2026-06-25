- **Fixed: `Array#index`, `Array#find_index`, and `Array#rindex` accept both a
  value and a block like Ruby.** `[1, 2, 3].index { |x| x > 1 }` and
  `[1, 2, 3, 2].rindex { |x| x == 2 }` now return the matching index instead of
  raising, and `find_index(value)` now accepts a value argument. Each method
  takes either a value (with Vibescript's optional non-negative offset) or a
  block, never both; passing both now raises. The nil-on-miss behavior is
  unchanged.
