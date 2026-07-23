- **Improved: class predicates narrow nominal unions.** `is_a?`, `kind_of?`,
  and `instance_of?` guards against statically resolved classes and modules
  now refine known union locals in both branches (guard clauses included), so
  guarded nominal code satisfies stricter known-union boundaries. Overridden
  predicates, module-typed arms, and dynamic receivers stay unchanged.
