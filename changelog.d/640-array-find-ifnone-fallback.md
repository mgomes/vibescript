- **Added: Ruby-style optional `ifnone` fallback for `Array#find`.** `find` now
  accepts an optional callable as its single positional argument. When the block
  finds no matching element, the callable is invoked with no arguments and its
  result is returned; a match always returns the element and never invokes the
  fallback. The no-argument miss still returns `nil`. The fallback must be a
  callable value (a function value works today), and a non-callable argument
  raises an error.
