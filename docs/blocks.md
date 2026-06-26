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

Ruby-style ampersand block forwarding (`&block`) and symbol-to-proc shorthand
(`&:method_name`) are not supported. Write an explicit `do ... end` or brace
block instead.

Reference scripts live in `examples/blocks/` and `examples/hashes/` (for merge
and reporting helpers).
