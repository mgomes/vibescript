- **Added: Ruby-style `Array#append` and `Array#prepend`.** `append` is an alias
  for `push`, returning a new array with the given values added to the end in
  order. `prepend` returns a new array with the values inserted at the front in
  order, so `[3].prepend(1, 2)` is `[1, 2, 3]`. Both keep Vibescript's
  non-mutating collection model (the receiver is left untouched) and reject
  keyword arguments.
