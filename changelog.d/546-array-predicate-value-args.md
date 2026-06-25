- **Added: Ruby-style pattern arguments for `Array#any?`, `Array#all?`, and
  `Array#none?`.** These predicates now accept an optional `pattern` argument
  alongside their existing no-argument and block forms: `any?(pattern)` is true
  when any element matches, `all?(pattern)` when every element matches, and
  `none?(pattern)` when no element matches. As in Ruby, the argument is tested
  with case equality (`===`), so range patterns such as `any?(1..3)` test
  membership rather than object identity. As with `count(value)`, a `pattern`
  argument takes precedence over an attached block, which is then ignored.
