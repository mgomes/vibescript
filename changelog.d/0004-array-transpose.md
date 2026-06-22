- **Added: Ruby-style `Array#transpose`.** `transpose` swaps the rows and
  columns of a matrix made of equal-length array rows, so
  `[[1, 2], [3, 4]].transpose` returns `[[1, 3], [2, 4]]`. An empty array
  transposes to `[]`, and rows of zero length collapse to no columns. It
  rejects extra arguments, raises when any element is not an array, and
  raises when the rows differ in length, reporting the offending index.
