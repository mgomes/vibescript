- **Added: Ruby visibility directives in class bodies.** `public`, `private`,
  and `protected` work as section directives, inline modifiers (including on
  `property`/`getter`/`setter` declarations), and retroactive symbol
  directives (`private :hidden, :other`). Protected methods allow explicit
  receivers when the caller's `self` is an instance of the same class,
  enforced across member, operator, index, and setter dispatch.
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
  ancestor order: own definitions win, later includes beat earlier ones,
  `include A, B` prefers `A`, and re-including a module already in the
  ancestry is a no-op. `is_a?`/`kind_of?` and class type contracts recognize
  included modules, including transitively included ones; `extend self`,
  including a class, and referencing an undeclared module fail with targeted
  diagnostics.
- **Changed: a bare `private` section now covers every following definition
  until another section directive, matching Ruby.** Previously it applied
  only to the next method definition.
- **Changed: `private :name` in a class body now retroactively makes the
  named method private.** Previously the symbol argument was accepted but
  inert, so code that relied on it kept the method public; the same code now
  dispatches the method as private.
- **Changed: `module`, `public`, `protected`, `include`, and `extend` are
  contextual keywords in declaration and directive positions.** Previously
  they were plain identifiers everywhere, so a parenless call to a user
  function of the same name could occupy those positions (`protected :b` in
  a class body called `def protected(...)`; `module Config` called
  `def module(...)`). Such scripts no longer run under the old meaning: a
  bare visibility or mixin directive that collides with a same-named script
  function is now a compile error naming the collision, and reinterpreted
  `module` shapes fail with targeted parse or resolution errors.
  Parenthesized calls (`public(:b)`) keep working, assignments
  (`public = 1`) still bind locals, and a bare word naming a local in scope
  still reads the local.
