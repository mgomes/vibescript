# Blocks and Enumerables

Blocks behave like lightweight lambdas. Use them with array helpers and
capability methods that enumerate results:

```vibe
def active_names(players)
  players
    .select do |player|
      player[:active]
    end
    .map do |player|
      player[:name]
    end
end
```

Fancy patterns like `reduce` or capability-driven `db.each` build on the same
mechanics. The interpreter ensures block parameters default to `nil` when fewer
values are provided, so you can write succinct loops.

Block parameters can destructure the yielded value. Missing entries bind to
`nil`, and `*rest` captures remaining entries, matching assignment
destructuring:

```vibe
pairs.map do |(left, right)|
  left + right
end
```

A bare `*` is an anonymous rest target that discards the values it captures
without binding a name, just as in assignment destructuring. It can sit at the
front, middle, or end of the pattern:

```vibe
rows.map do |(head, *)|
  head
end

rows.map do |(head, *, tail)|
  [head, tail]
end
```

## Returning from the enclosing method

As in Ruby, an explicit `return` inside a normal block returns from the method
whose body created the block, not just from the block call, so iteration ends
immediately:

```vibe
def first_even(values)
  values.each do |value|
    if value % 2 == 0
      return value
    end
  end
  "none"
end

first_even([1, 2, 3]) # => 2
```

The return unwinds like an error — `ensure` blocks on the way out still run —
but no `rescue` clause can intercept it, and a typed method validates the value
against its return type as usual. A block invoked after its method has already
returned (for example, one a host adapter stored) has no frame to return from,
so an explicit `return` in it raises `LocalJumpError`, matching Ruby. To leave
just the block with a value, make the value the block's last expression.

## Detecting and invoking a supplied block

A function or method receives a block from its caller and runs it with `yield`.
`yield` raises `no block given` when the call supplied no block, so optional
block APIs first ask `block_given?` (Ruby's Kernel predicate). It returns `true`
when the current call was given a block and `false` otherwise, letting a method
branch instead of raising:

```vibe
def fetch(default)
  if block_given?
    yield
  else
    default
  end
end

fetch("none")            # => "none"
fetch("none") { "value" } # => "value"
```

`block_given?` reads the block of the call that is currently running. It is
`false` at the top level and in any call that received no block, and a nested
call does not inherit its caller's block. Inside a block, `block_given?` reports
the enclosing method's block, matching Ruby. The predicate is reserved and
cannot be shadowed by a local; the parenthesized `block_given?()` form behaves
the same and, like Ruby, accepts no arguments.

## Procs and lambdas

Blocks become first-class values through Ruby's callable constructors:
`Proc.new { ... }`, `proc { ... }`, `lambda { ... }`, and the stabby lambda
`->(args) { ... }` (which also accepts a `do ... end` body). All four produce
values invoked with `.call`, and they pass through function-typed parameters
like any other callable:

```vibe
def run
  double = ->(n) { n * 2 }
  add = lambda do |a, b|
    a + b
  end
  add.call(double.call(20), 2) # => 42
end
```

Procs and lambdas differ exactly as in Ruby:

- A **proc** (`proc { }` / `Proc.new { }`) keeps block semantics. Missing
  arguments pad to `nil`, extra arguments are dropped, a single array argument
  auto-splats across multiple parameters, and `return` in the body returns
  from the method whose body created the proc. Calling such a proc after that
  method has already returned raises `LocalJumpError`.
- A **lambda** (`lambda { }` / `->() { }`) behaves like an anonymous method.
  Arity is strict — `->(a, b) { }.call(1)` raises
  `lambda expects 2 arguments, got 1` — and `return`, `break`, and `next` in
  the body are all local: they end the lambda call with a value instead of
  unwinding the enclosing method.

`fn.lambda?` reports which semantics a callable value carries. Lambda
parameters use the block-parameter grammar, so type annotations
(`->(x: Int) { ... }`) and destructuring targets work, and a lambda without a
parameter list infers implicit parameters (`it`, `_1`..`_9`) just as blocks
do. The `-> Type` return annotation on `def` still parses; it must sit on the
signature line, while a `->` opening the next line starts a lambda literal.

Ruby-style ampersand block forwarding (`&block`) and symbol-to-proc shorthand
(`&:method_name`) are not supported. Write an explicit `do ... end` or brace
block instead.

Reference scripts live in `examples/blocks/` and `examples/hashes/` (for merge
and reporting helpers).
