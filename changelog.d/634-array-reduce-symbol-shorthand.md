- **Added: Ruby-style symbol shorthand for `Array#reduce`.** `reduce(operation)`
  and `reduce(initial, operation)` fold by sending `operation` to the
  accumulator with each element, matching Ruby's `[1, 2, 3].reduce(:+)`.
  `operation` is a symbol or string naming a binary operator (`"+"`, `"-"`,
  `"*"`, `"/"`, `"%"`, `"**"`) or a method on the accumulator
  (`["a", "b"].reduce(:concat)`); operator-name symbols such as `:+` route
  through the same path once they parse as symbol literals. A block still takes
  precedence, so a lone argument alongside a block is treated as the initial
  value. An empty array now folds to `nil` (or to the supplied `initial`)
  instead of raising, matching Ruby's `[].reduce { ... }`.
