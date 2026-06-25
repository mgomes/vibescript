- **Added: Ruby-style value arguments for `Array#any?`, `Array#all?`, and
  `Array#none?`.** These predicates now accept an optional `value` argument
  alongside their existing no-argument and block forms: `any?(value)` is true
  when any element matches, `all?(value)` when every element matches, and
  `none?(value)` when no element matches. Ruby tests these arguments with case
  equality (`===`); until the broader case-equality work lands the `value` form
  uses Vibescript's value equality, the same matcher as `count(value)` and
  `include?`. As with `count(value)`, a `value` argument takes precedence over an
  attached block, which is then ignored.
