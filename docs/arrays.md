# Arrays in VibeScript

Arrays are ordered collections. Use square brackets to declare literals:

```vibe
players = ["alex", "maya", "li"]
```

## Transformations

Common enumerable helpers include:

- `map` to transform elements.
- `select` to filter items.
- `reduce` to accumulate values.
- `first(n)` / `last(n)` to slice without mutating.
- `push`/`pop` for building or removing values while keeping the original array untouched.
- `sum` to total numeric arrays.
- `compact` to drop `nil` entries.
- `flatten(depth = nil)` to collapse nested arrays (defaults to fully flattening).
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
