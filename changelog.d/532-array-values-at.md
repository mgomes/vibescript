- **Added: Ruby-style `Array#values_at`.** `values_at(*indexes)` reads several
  elements at once, returning a new array in the order the indexes were
  requested. Negative indexes count back from the end, out-of-bounds indexes
  yield `nil`, and duplicate indexes repeat their values, so the result always
  has one entry per argument. Float indexes truncate toward zero like Ruby's
  `to_int` conversion; non-numeric indexes and keyword arguments raise.
