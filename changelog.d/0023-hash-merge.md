- **Added: Ruby-style conflict blocks for `Hash#merge`.** `Hash#merge` now
  honors an optional block to resolve key conflicts: for keys present in both
  hashes the block is yielded `(key, old_value, new_value)` and its result is
  stored, while keys present on only one side are copied without invoking the
  block. Without a block the incoming hash still wins on conflicts. The conflict
  key is yielded as a symbol, matching the other hash helpers, and the block was
  previously accepted but silently ignored.
