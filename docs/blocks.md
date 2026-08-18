# Blocks and Enumerables

A block is syntax attached to a call, not a value: the call runs it with
`yield`, synchronously, and the block cannot outlive the call it was given
to. Use blocks with array helpers and capability methods that enumerate
results:

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
against its return type as usual. To leave just the block with a value, make
the value the block's last expression.

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

## Blocks do not escape

Executable code is not a value. There are no proc or lambda constructors, no
stabby-lambda literals, no first-class function or bound-method values, no
callable `.call`, and no block capture or forwarding with `&` — each of those
spellings is a compile error that names the replacement. Named functions and
methods remain directly callable; what was a callable value becomes either a
named function called where it is needed, or a block written at the call that
runs it:

```vibe
def double(n)
  n * 2
end

def run
  [1, 2, 3].map { |n| double(n) }  # => [2, 4, 6]
end
```

The Ruby shorthands migrate the same way: `words.map(&:upcase)` becomes
`words.map { |word| word.upcase }`, and `numbers.reduce(&:+)` becomes
`numbers.reduce { |total, n| total + n }`. A method that forwarded its
caller's block with `&param` instead runs it in place with `yield` and asks
`block_given?` when the block is optional.

The runtime enforces the lifetime rather than documenting it: when the call a
block was given to returns, the block is retired, and invoking it later —
however it was retained, including by a host adapter — raises
`block invoked after the call it was given to returned`. Within the call the
block may run any number of times; the rule is about lifetime, not use count.

Reference scripts live in `examples/blocks/` and `examples/hashes/` (for merge
and reporting helpers).
