- **Added: `Array#flat_map` (alias `collect_concat`).** `map { }.flatten(1)`
  covered it, so this is the missing shorthand for a common shape -- notable
  mainly because `filter_map` was already present, which made the absence look
  arbitrary. It flattens exactly one level, as in Ruby.
