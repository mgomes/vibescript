- **Added: Ruby-style symbol shorthand for `Array#reduce`.** `reduce(operation)`
  and `reduce(initial, operation)` fold by sending `operation` to the
  accumulator with each element, matching Ruby's
  `["a", "b"].reduce(:concat)`. `operation` is a symbol naming a method on the
  accumulator (`["a", "b"].reduce(:concat)`) or a string naming a method or a
  binary operator (`[1, 2, 3].reduce("+")`, also `"-"`, `"*"`, `"/"`, `"%"`,
  `"**"`). A block still takes precedence, so a lone argument alongside a block
  is treated as the initial value. An empty array now folds to `nil` (or to the
  supplied `initial`) instead of raising, matching Ruby's `[].reduce { ... }`.
  Operator-symbol literals such as `:+` are not yet accepted here because the
  lexer cannot tokenize them; that shorthand is tracked in #801.
