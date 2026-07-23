- **Added: the `is_type?` predicate.** `value.is_type?(:int)` tests any value
  against a type atom — a primitive (`:int`, `:string`, `:number`, …), a bare
  container (`:array`, `:hash`, `:range`, `:function`), a class or enum name,
  or a nullable form (`'int?'`) — without coercion. The checker gives it a
  typed contract and narrows known union locals through both branches of a
  literal-atom test.
