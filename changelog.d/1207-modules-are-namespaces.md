- **Changed: modules are namespaces, not behavior injection.** `include` and
  `extend` are removed, along with instance-style module methods, module
  accessors and aliases, the constants an include copied into the including
  class, and the `is_a?`/type relationships inclusion created. A module holds
  constants, nested modules, and `def self.` functions, and `Outer::Inner`
  scoped resolution is unchanged. Classes remain, still without inheritance.
  Mixins introduced a hidden method and constant source, a collision order, a
  transitive membership graph, visibility copying, and per-execution accounting
  for adopted state; explicit namespace calls provide the same reuse with the
  dependency visible at the call site
  ([ADR-006](docs/adr/006-slim-language-for-predictable-sandboxing.md)).

  Migration: move each mixed-in method to `def self.name(receiver, ...)` on the
  module and call it there, so `person.display_name` becomes
  `Naming.display_name(person)`. Read a module's constants through the module
  (`Limits::MAX`) rather than by the bare name an include used to supply. Replace
  an `is_a?(SomeModule)` test or a `(value: SomeModule)` annotation with the
  concrete class, a union of classes, or a duck-typed check. `include` and
  `extend` in a class or module body, and a plain `def`, `property`, `getter`,
  `setter`, or `alias` in a module body, are compile errors naming the
  replacement.
