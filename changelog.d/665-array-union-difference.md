- **Added: Ruby-style `Array#union` and `Array#difference`.** `union(*others)`
  concatenates the receiver with every argument array and removes duplicates,
  keeping the first occurrence of each value (with no arguments it deduplicates
  the receiver). `difference(*others)` returns the receiver's elements that do
  not appear in any argument array while preserving the receiver's own
  duplicates. Both compare values by content (so nested arrays and hashes match
  like `uniq`), return a new array without mutating the receiver, and raise when
  handed a non-array argument.
