- **Improved: static checks compare resolved named types.** Different resolved
  enums and classes are now incompatible with each other and with unrelated
  primitives and containers at typed boundaries, while symbols keep coercing
  into enums, classes keep satisfying included modules, and unresolved or
  host-supplied names stay conservative.
