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

Reference scripts live in `examples/blocks/` and `examples/hashes/` (for merge
and reporting helpers).
