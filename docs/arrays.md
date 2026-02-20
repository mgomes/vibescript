# Arrays in VibeScript

Arrays are ordered collections. Use square brackets to declare literals:

```vibe
players = ["alex", "maya", "li"]
```

## Transformations

Common enumerable helpers include:

- `map` to transform elements.
- `select` to filter items.
- `find` / `find_index` to locate the first matching item.
- `reduce` to accumulate values.
- `first(n)` / `last(n)` to slice without mutating.
- `push`/`pop` for building or removing values while keeping the original array untouched.
- `sum` to total numeric arrays.
- `compact` to drop `nil` entries.
- `flatten(depth = nil)` to collapse nested arrays (defaults to fully flattening).
- `chunk(size)` to split into fixed-size slices.
- `window(size)` to build overlapping windows.
- `join(sep = "")` to produce a string.

Example:

```vibe
def total_by_multiplier(values, multiplier)
  values
    .map do |value|
      value * multiplier
    end
    .sum()
end
```

```vibe
[1, 2, 3, 4, 5].chunk(2)   # [[1,2], [3,4], [5]]
[1, 2, 3, 4].window(3)      # [[1,2,3], [2,3,4]]
```

## Search and predicates

- `include?(value)` for membership checks.
- `index(value, offset = 0)` / `rindex(value, offset = last_index)` for positional lookup.
- `count`, `count(value)`, or `count { ... }`.
- `any?`, `all?`, `none?` with optional blocks.

```vibe
def health_checks(values)
  {
    has_zero: values.include?(0),
    first_large_idx: values.index(100),
    all_non_negative: values.all? { |v| v >= 0 }
  }
end
```

## Ordering and grouping

- `reverse`, `sort`, and `sort_by`.
- `partition` to split into matching and non-matching arrays.
- `group_by` to collect values by key.
- `group_by_stable` to collect values by key while preserving group order.
- `tally` to count symbol/string occurrences.

Sorting of strings/symbols uses deterministic codepoint ordering (locale
collation is not applied).

```vibe
def summarize(players)
  grouped = players.group_by { |p| p[:status] }
  counts = players.map { |p| p[:status] }.tally

  {
    by_status: grouped,
    totals: counts
  }
end
```

## Set-like Operations

Use `+` to concatenate and `-` to subtract values:

```vibe
def unique_participants(core, late)
  (core + late).uniq().compact()
end

def without_dropouts(participants, dropouts)
  participants - dropouts
end
```

See `examples/arrays/` for concrete scripts exercised by the test suite.
