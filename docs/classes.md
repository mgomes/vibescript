# Classes

Vibescript classes group related state and behavior using instance methods,
class methods, instance variables, and class variables.

Inheritance is not supported. Class definitions do not support
subclassing or `super` calls.

## Defining A Class

Use `class ... end` to declare a class and `def ... end` for methods:

```vibe
class Counter
  def initialize(start)
    @count = start
  end

  def value
    @count
  end

  def increment
    @count = @count + 1
  end
end
```

Construct instances with `.new`:

```vibe
counter = Counter.new(10)
counter.increment
counter.value
```

If `initialize` is defined, `.new` forwards arguments to it.

## Instance Methods vs Class Methods

Instance methods:

- Are declared with `def name`.
- Are called on instances (`user.name`).

Class methods:

- Are declared with `def self.name`.
- Are called on the class (`User.find(1)`).

```vibe
class Mathy
  def self.twice(n)
    n * 2
  end

  def call_twice(n)
    self.class.twice(n)
  end
end
```

## Instance Variables (`@name`)

Instance variables are per-object state:

```vibe
class Person
  def initialize(name)
    @name = name
    @age = 0
  end

  def birthday
    @age = @age + 1
  end
end
```

Shorthand parameter assignment is supported in method signatures:

```vibe
class Point
  def initialize(@x, @y)
  end
end
```

## Class Variables (`@@name`)

Class variables are shared by all instances of the same class within a script
invocation:

```vibe
class Counter
  @@instances = 0

  def initialize
    @@instances = @@instances + 1
  end

  def self.instances
    @@instances
  end
end
```

## `property`, `getter`, And `setter`

Inside a class body, you can generate accessor methods:

- `property x` creates `x` and `x=`.
- `getter x` creates `x`.
- `setter x` creates `x=`.

```vibe
class Account
  property balance
  getter owner
  setter nickname

  def initialize(owner, balance)
    @owner = owner
    @balance = balance
    @nickname = ""
  end
end
```

When assigning through a member (`obj.name = ...`):

- If `name=` exists, Vibescript calls that setter method.
- If only `name` exists (getter without setter), assignment raises a read-only
  property error.

### Typed accessors

Accessor declarations accept a type annotation, and the generated methods enforce
the same runtime boundary checks as a handwritten getter or setter. The type binds
to each name individually, so a comma-separated declaration can mix types:

```vibe
class User
  property name: string
  getter age: int?
  property x: int, y: int
end
```

`property name: string` generates a `name -> string` getter and a
`name=(value: string)` setter; `getter`/`setter` generate the matching half. A typed
setter rejects a wrong-typed assignment, and a typed getter enforces its return type
on read — so reading a property whose backing ivar was never set raises unless the
type is nullable (e.g. `int?`). Bare accessors without an annotation stay untyped.

The declared type also guards the backing instance variable itself: a direct
write such as `@name = value` — in a constructor, an ordinary method, or through
an `@name` parameter — normalizes and validates like the generated setter's
boundary, raising `instance variable @name expected string, got int` when the
value is incompatible. The contract is the generated setter's parameter type
when a generated setter exists, otherwise the generated getter's return type. A
handwritten setter takes over the write path, so direct writes stay dynamic
even beside a generated typed getter, and instance variables without a typed
generated accessor stay fully dynamic.

## Operator And Index Methods

As in Ruby, a class can define operator methods and the index protocol, and
operator syntax dispatches to them on the receiver (the left operand):

```vibe
class Vec
  def initialize(x)
    @x = x
  end
  def x
    @x
  end
  def +(other)
    Vec.new(x + other.x)
  end
  def ==(other)
    x == other.x
  end
end

class Counter
  def initialize
    @slots = {}
  end
  def [](key)
    @slots.fetch(key, 0)
  end
  def []=(key, value)
    @slots[key] = value
  end
end

(Vec.new(1) + Vec.new(2)).x  # => 3
Vec.new(1) == Vec.new(1)     # => true

c = Counter.new
c["a"] = 1
c["a"] += 5
c["a"]                       # => 6
```

Definable names: `+`, `-`, `*`, `/`, `%`, `**`, `<<`, `&`, `==`, `!=`, `<`,
`<=`, `>`, `>=`, `<=>`, `[]`, and `[]=`. `[]` may take multiple indices
(`grid[row, col]`), and `[]=` receives the indices followed by the assigned
value. When a class defines `==` without `!=`, `!=` is its negation; without
either, instances keep built-in identity equality. Operator methods are
instance methods only — a top-level or `self.` operator definition is a
compile error — and dispatch ignores the right operand's class, matching
Ruby's left-receiver rule.

## Visibility

Methods are public by default. Class bodies support the Ruby visibility
directives in three forms: inline modifiers, section directives, and symbol
directives.

Inline modifiers apply to a single declaration:

```vibe
class Helper
  private def secret
    42
  end

  private property token: string

  def call_internal
    secret
  end
end
```

A visibility word on its own line starts a section: every declaration that
follows it — methods, `def self.` class methods, and
`property`/`getter`/`setter` accessors alike — takes that visibility until
another section directive:

```vibe
class Secret
  private

  def hidden
    "hidden"
  end

  def also_hidden
    "hidden too"
  end

  public

  def shown
    hidden
  end
end
```

Symbol directives change the visibility of methods that are already defined:

```vibe
class Secret
  def hidden
    "hidden"
  end

  private :hidden
end
```

`public :name`, `private :name, :other`, and `protected :name` are all
supported; naming a method that has not been defined is a compile error.

### Private

Private methods are callable only with an implicit receiver. Inside a method,
call `secret`, not `self.secret`; explicit receiver calls like `self.secret` or
`other.secret` raise a runtime `private method` error.

Private class methods use the same forms with a `self.` method name:

```vibe
class Helper
  private def self.build_secret
    42
  end

  def self.build
    build_secret
  end
end
```

Vibescript does not support Ruby's singleton-class syntax (`class << self`).
Use `def self.name` or `private def self.name` inside the class body.

### Protected

Protected methods allow explicit receivers, but only when the caller's `self`
is itself an instance of the same class — the Ruby idiom for comparing two
instances' internals (Vibescript has no inheritance, so Ruby's "same class or
subclass" reduces to "same class"):

```vibe
class Account
  def initialize(balance)
    @balance = balance
  end

  def richer_than?(other)
    balance > other.balance
  end

  protected

  def balance
    @balance
  end
end
```

`Account.new(10).richer_than?(Account.new(3))` returns `true`, while calling
`account.balance` anywhere outside the class raises a runtime
`protected method balance` error. A protected class method is callable only
from other class methods of the same class. Operator and index methods
(`def +`, `def []`) honor `private` and `protected` like any other method.
Protected access is scoped to the exact class definition: two classes that
include the same module are not a family, so an instance of one cannot call
protected methods on an instance of the other (in Ruby, the shared module
ancestor would grant that access).

One documented divergence from Ruby: a visibility section also covers
`def self.` class methods declared after it, whereas Ruby scopes sections to
instance methods only.

## Module Declarations

`module Name ... end` declares a namespace in source, alongside the file-based
modules that `require` loads. A module groups module functions (`def self.`)
and constants:

```vibe
module Billing
  LIMIT = 5

  def self.code
    "ok"
  end

  def self.limit
    LIMIT
  end
end

Billing.code       # "ok"
Billing.limit      # 5
Billing::LIMIT     # 5
```

Constants are visible inside the module's methods by bare name and outside
via `Module::CONST` (or `Module.CONST`). Like class bodies, module bodies run
once per script invocation and module state is isolated between invocations —
one call's constant mutations never leak into the next.

Modules nest, and nested modules resolve through the scope operator:

```vibe
module Outer
  module Inner
    BASE = 2

    def self.double
      BASE * 2
    end
  end
end

Outer::Inner.double    # 4
Outer::Inner::BASE     # 2
```

Rules and limits:

- `module` is contextual, not a reserved keyword: it starts a declaration only
  when followed by a name on the same line. Module names must start with an
  uppercase letter.
- Declarations are allowed at the top level and nested inside module bodies.
  A module declaration inside a class or function body is a parse error, as is
  a class declaration inside a module body.
- Modules cannot be instantiated: `Billing.new` raises
  `module Billing cannot be instantiated`.
- A module may also declare instance-style methods (plain `def`); they are not
  callable on the module itself — they exist to be mixed into classes with
  `include`/`extend` (see Mixins below).

## Mixins: `include` And `extend`

Class bodies (and module bodies) accept Ruby's mixin directives. `include`
mixes a module's instance-style methods into the class's instance methods;
`extend` mixes them into the class's own (`self.`) methods:

```vibe
module Named
  def display_name
    "I am " + name
  end
end

class Person
  include Named

  def initialize(name)
    @name = name
  end

  def name
    @name
  end
end

Person.new("Ada").display_name    # "I am Ada"
```

The directive references a module declared earlier in source (file-based
`require` namespaces cannot be included). Multiple modules may be listed
(`include A, B`), names may be scope-qualified (`include Support::Naming`),
and inside a module body a sibling nested module resolves by short name.

Vibescript applies mixins by copying method definitions at compile time, with
collision rules that match Ruby's ancestor ordering:

- the class's own definitions always win over included methods, wherever the
  `include` appears in the body;
- a later `include` wins over an earlier one;
- within one directive, earlier modules win (`include A, B` behaves as if `B`
  were included first).

What carries over:

- **Visibility** — a module's private/protected methods stay private/protected
  in the including class, and the class may retarget them
  (`public :name`) without affecting the module. Protected access stays
  scoped to each including class: two classes that include the same module
  cannot call each other's protected methods (unlike Ruby, where the shared
  module ancestor grants access).
- **Operator and index methods** — `def +`, `def []`, `def []=` defined in a
  module dispatch on instances of the including class.
- **Accessors** — `property`/`getter`/`setter` declared in a module generate
  accessor methods that copy like any other method.
- **Constants** — an included module's constants surface as class constants
  (`Config::MAX`) and are readable by bare name inside methods. The class's
  own constants win; mutations stay in the class's per-call copy and never
  write back to the module.
- **Ancestry** — `is_a?`/`kind_of?` report `true` for included modules
  (`instance_of?` stays exact-class), and a module name used as a type
  contract (`def describe(thing: Named)`) accepts instances of any class that
  includes it.

Modules can `include` other modules; the composed method set, constants, and
ancestry all flow through transitively — a class including the outer module
also `is_a?` every module that module includes, and matches them as type
contracts. Re-including a module that is already in the ancestry is a no-op,
as in Ruby: it does not restore the module's methods or constants over a
later include's. `extend` copies the module's instance-style methods to the
class surface only — it does not adopt constants and does not affect `is_a?`.

Not supported (each fails with a targeted diagnostic): `extend self` (define
module functions with `def self.name` instead), including a class, and
referencing a module that has not been declared yet.

## Introspection

Instances respond to the Ruby-style introspection predicates `is_a?`,
`kind_of?`, `instance_of?`, and `respond_to?`:

```vibe
class User
end

user = User.new
user.is_a?(User)            # true
user.instance_of?(User)     # true
user.respond_to?(:greet)    # false  (no such method)
```

`is_a?` and `kind_of?` test the instance's own class plus its module ancestry:
they report `true` for the exact class and for any module the class includes,
directly or through another module's `include` (see
[Mixins](#mixins-include-and-extend)). `instance_of?` tests the exact class
only, so it disagrees with `is_a?` precisely when the argument is an included
module. There is no class inheritance yet. `respond_to?` reports public methods;
private and protected methods report `false` externally but `true` when the
receiver checks itself (or when called with `respond_to?(name, true)`). Instance variables are
attributes, not methods, so they never respond. These predicates are documented
in full in [stdlib_core_utilities.md](stdlib_core_utilities.md#object-introspection).

## Common Errors

- Calling a missing method: `unknown member ...` / `unknown class member ...`
- Calling a private method externally: `private method ...`
- Calling a protected method outside the class family: `protected method ...`
- Assigning to getter-only attributes: `cannot assign to read-only property ...`
- Calling `.new` with wrong arguments for `initialize`: argument errors
