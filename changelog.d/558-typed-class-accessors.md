- **Added: type annotations on generated class accessors.** `property`,
  `getter`, and `setter` declarations now accept a type annotation, and the
  generated methods enforce the same runtime boundary checks as handwritten
  getters and setters. `property name: string` generates a `name -> string`
  getter and a `name=(value: string)` setter, `getter`/`setter` generate the
  matching half, and the type binds per name so a comma-separated declaration
  (`property x: int, y: string`) can mix types while bare accessors stay
  untyped.
