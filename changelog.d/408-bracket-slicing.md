- **Added: Ruby-style negative indexes and bracket slicing for arrays and
  strings.** Bracket access now mirrors `Array#[]` and `String#[]`: a single
  index counts a negative value back from the end and returns `nil` when out of
  range rather than raising (`[10, 20, 30][-1]` is `30`, `[1][5]` is `nil`),
  `value[start, length]` returns a subarray or substring, and `value[range]`
  slices by an integer range. Array assignment accepts a negative index
  (`array[-1] = value` updates the last element) but still raises when the index
  falls outside the array. String indexing remains rune-aware.