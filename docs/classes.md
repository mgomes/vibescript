# Classes

VibeScript classes group related state and behavior using instance methods,
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

- If `name=` exists, VibeScript calls that setter method.
- If only `name` exists (getter without setter), assignment raises a read-only
  property error.

## Privacy

Mark methods private with `private def`:

```vibe
class Helper
  private def secret
    42
  end

  def call_internal
    secret
  end
end
```

Private methods are callable only on the current receiver (the same instance or
class context). Calls like `other.secret` raise a runtime `private method`
error.

## Common Errors

- Calling a missing method: `unknown member ...` / `unknown class member ...`
- Calling a private method externally: `private method ...`
- Assigning to getter-only attributes: `cannot assign to read-only property ...`
- Calling `.new` with wrong arguments for `initialize`: argument errors
