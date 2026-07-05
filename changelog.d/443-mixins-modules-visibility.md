- **Added: Ruby visibility directives in class bodies.** `public`, `private`,
  and `protected` work as section directives, inline modifiers (including on
  `property`/`getter`/`setter` declarations), and retroactive symbol
  directives (`private :hidden, :other`). Protected methods allow explicit
  receivers when the caller's `self` is an instance of the same class,
  enforced across member, operator, index, and setter dispatch.
- **Changed: a bare `private` section now covers every following definition
  until another section directive, matching Ruby.** Previously it applied
  only to the next method definition.
- **Added: Ruby-style module namespace declarations.** `module Name ... end`
  declares an in-source namespace: `def self.` module functions dispatch as
  `Billing.code`, constants resolve as `Billing::LIMIT`, modules nest
  (`Outer::Inner`), and module state is isolated per script invocation.
  Modules cannot be instantiated, and misplaced declarations get targeted
  parse errors.
- **Added: `include`/`extend` mixins in class and module bodies.** `include`
  mixes a module's instance methods (visibility, operator/index methods, and
  accessors included) into a class and surfaces its constants as class
  constants; `extend` adds them as class methods. Collisions follow Ruby's
  ancestor order: own definitions win, later includes beat earlier ones, and
  `include A, B` prefers `A`. `is_a?`/`kind_of?` and class type contracts
  recognize included modules; `extend self`, including a class, and
  referencing an undeclared module fail with targeted diagnostics.
