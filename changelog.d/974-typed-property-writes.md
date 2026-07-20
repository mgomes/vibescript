- **Fixed: direct instance-variable writes honor typed property contracts.**
  `@name = value` inside any method now normalizes and validates against the
  type declared by a generated `property`/`getter`/`setter` accessor when the
  write executes, instead of failing only when a later typed getter or
  boundary observed the value. Compound, logical, destructuring, and `@ivar`
  constructor-parameter writes validate the same way; untyped accessors and
  undeclared instance variables stay fully dynamic.
