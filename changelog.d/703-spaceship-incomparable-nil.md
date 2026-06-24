- **Changed: the spaceship operator `<=>` now returns `nil` for incomparable
  operands** instead of raising, matching Ruby. Mixed-kind pairs such as
  `1 <=> "a"`, money values in different currencies, and `Time#<=>` against a
  non-`Time` now yield `nil`, while comparable pairs still return `-1`/`0`/`1`.
  The relational operators `<`, `<=`, `>`, `>=` keep raising on incomparable
  operands, matching Ruby's `ArgumentError`.
