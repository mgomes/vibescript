- **Fixed: `eql?` was not kind-strict for nested values.** `[1].eql?([1.0])` was
  true where Ruby says false: `eql?` checked only the outermost kind and then
  delegated to `==`, so elements took the numeric comparison. Strictness now
  holds at every level, which is what hash-key identity depends on. `==` is
  unchanged and stays numeric nested.
