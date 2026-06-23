- **Fixed: `Time#eql?` and `Duration#eql?` now behave like Ruby predicates for
  wrong-type operands.** Both methods return `false` when given an operand of the
  wrong kind (for example `time.eql?(1)` or `duration.eql?(Time.utc(2024, 1,
  1))`) instead of raising a type error, matching Ruby's `Time#eql?`. Equal
  same-kind operands still return `true`, unequal ones `false`, and only the
  wrong number of arguments raises an argument-count error.
