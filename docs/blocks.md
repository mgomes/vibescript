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

Ruby-style ampersand block forwarding (`&block`) and symbol-to-proc shorthand
(`&:method_name`) are not supported. Write an explicit `do ... end` or brace
block instead.

Reference scripts live in `examples/blocks/` and `examples/hashes/` (for merge
and reporting helpers).
