- **Added: Ruby-style case equality operator `===`.** Vibescript now parses and
  evaluates `===`, treating its left operand as a matcher and its right operand
  as the value being tested, mirroring `case`/`when` matching. Range matchers
  check membership (`(1..3) === 2` is `true`, `(1...3) === 3` is `false`) and
  every other matcher falls back to `==` (`1 === 1` is `true`, `2 === (1..3)` is
  `false`). Because the scalar path reuses `==`, integers and floats stay
  distinct kinds, so `1 === 1.0` is `false`, unlike Ruby.
