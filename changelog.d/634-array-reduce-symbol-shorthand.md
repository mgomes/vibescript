- **Added: Ruby-style symbol shorthand for `Array#reduce`.** `reduce(operation)`
  and `reduce(initial, operation)` fold by sending `operation` to the
  accumulator with each element, matching Ruby's
  `["a", "b"].reduce(:concat)`. `operation` is a symbol naming a method on the
  accumulator (`["a", "b"].reduce(:concat)`) or a string naming a method or a
  binary operator (`[1, 2, 3].reduce("+")`, also `"-"`, `"*"`, `"/"`, `"%"`,
  `"**"`). A block still takes precedence, so a lone argument alongside a block
  is treated as the initial value. An empty array now folds to `nil` (or to the
  supplied `initial`) instead of raising, matching Ruby's `[].reduce { ... }`.
  Method dispatch is public-only, mirroring Ruby's
  `accumulator.public_send(operation, item)`: a private method cannot be reached
  through the shorthand even when the accumulator is the current `self`.
  Operator-symbol literals such as `:+` are not yet accepted here because the
  lexer cannot tokenize them; that shorthand is tracked in #801.
