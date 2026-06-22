- **Fixed: Hash membership predicates align with Ruby.** `Hash#key?`,
  `Hash#has_key?`, and `Hash#include?` now return `false` for candidate keys of
  unsupported types instead of raising, matching Ruby's predicate semantics.
