- **Improved: the checker validates direct writes to typed properties.**
  Instance-method analysis now seeds facts for typed accessor-backed instance
  variables, so a direct write such as `@name = 1` against
  `property name: string` is reported when the value's known type provably
  contradicts the contract, and reads observe the declared type. Unknown
  values still pass and rely on the runtime guard; untyped accessors and
  undeclared instance variables stay dynamic.
