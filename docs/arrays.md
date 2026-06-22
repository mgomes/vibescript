# Arrays in Vibescript

Arrays are ordered collections. Use square brackets to declare literals:

```vibe
players = ["alex", "maya", "li"]
```

For static string or symbol lists, percent array literals are also supported:

```vibe
words = %w[alpha beta gamma]
statuses = %i[draft published archived]
```

## Transformations

Common enumerable helpers include:

- `map` to transform elements.
- `select` to keep items the block accepts.
- `reject` to keep items the block rejects (the inverse of `select`).
- `find` / `find_index` to locate the first matching item.
- `reduce` to accumulate values.
- `first(n)` / `last(n)` to slice without mutating.
- `take(n)` / `drop(n)` to keep or skip a prefix; both reject negative counts.
- `zip(*arrays)` to combine arrays element-wise into rows, padding short arrays with `nil`.
- `transpose` to swap the rows and columns of a matrix of equal-length array rows; it raises when a row is not an array or the rows differ in length.
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
    .sum
end
```

```vibe
[1, 2, 3, 4, 5].chunk(2)   # [[1,2], [3,4], [5]]
[1, 2, 3, 4].window(3)      # [[1,2,3], [2,3,4]]
[1, 2, 3].take(2)           # [1, 2]
[1, 2, 3].drop(1)           # [2, 3]
[1, 2].zip([3, 4], [5])     # [[1, 3, 5], [2, 4, nil]]
[[1, 2], [3, 4]].transpose  # [[1, 3], [2, 4]]
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

## Prefix and pattern filtering

- `take_while { ... }` keeps leading elements until the block first returns a
  falsy value, then stops. The block is never called again after the first miss.
- `drop_while { ... }` skips leading elements while the block returns truthy and
  returns the remainder, including every element after the first miss.
- `grep(pattern)` keeps elements that match `pattern` using Vibescript's
  case-equality direction (`pattern === element`), the same matcher used by
  `case`/`when`. A `Range` matches by membership; any other value matches by
  equality.
- `grep_v(pattern)` keeps the elements that do **not** match `pattern`.

Both `grep` and `grep_v` accept an optional block that transforms each kept
element before it is collected.

```vibe
[1, 2, 3, 4].take_while { |n| n < 3 }   # [1, 2]
[1, 2, 3, 4].drop_while { |n| n < 3 }   # [3, 4]
[1, 2, 3, 4].grep(2..3)                 # [2, 3]
[1, 2, 3, 4].grep_v(2..3)               # [1, 4]
[1, 2, 3, 4].grep(2..3) { |n| n * 10 }  # [20, 30]
["apple", "bee"].grep("bee")            # ["bee"]
```

Regular-expression patterns are not yet available, so `grep("e")` matches the
exact string `"e"` rather than any string containing it.

## Block iteration

These helpers yield to a block instead of building a result array. Each yielded
slice or window is an independent array, so mutating it never touches the
receiver.

- `each_slice(n)` yields non-overlapping slices of length `n`, including a
  shorter trailing slice when the length is not a multiple of `n`. `n` must be a
  positive integer. Returns `nil`.
- `each_cons(n)` yields every sliding window of length `n`; an array shorter than
  `n` yields nothing. `n` must be a positive integer. Returns `nil`.
- `reverse_each` yields values from last to first and returns the receiver.
- `cycle(n)` yields the whole array `n` times. A non-positive `n` yields nothing.
  Omitting `n` or passing `nil` cycles forever; the step quota and context
  cancellation bound the otherwise unbounded loop. Returns `nil`.

```vibe
def collect_slices(values, size)
  slices = []
  values.each_slice(size) do |slice|
    slices = slices.push(slice)
  end
  slices
end

collect_slices([1, 2, 3, 4, 5], 2)  # [[1, 2], [3, 4], [5]]
```

```vibe
[1, 2, 3, 4].each_cons(3) do |window|
  window.sum
end                                 # yields [1, 2, 3] then [2, 3, 4]
[1, 2, 3].reverse_each do |value|
  value * 10
end                                 # yields 3, 2, 1
[1, 2].cycle(2) do |value|
  value + 1
end                                 # yields 1, 2, 1, 2
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

## Extrema

- `min` / `max` return the smallest/largest element using the same comparison
  semantics as `sort`. They return `nil` for an empty array.
- `minmax` returns a `[min, max]` pair in one pass; an empty array yields
  `[nil, nil]`.
- `min_by { ... }` / `max_by { ... }` select the element whose block-derived key
  is smallest/largest, mirroring `sort_by`. They return `nil` for an empty
  array.

Ties resolve to the first matching element. Mixing incomparable values (for
example numbers with strings) raises an error, just like `sort`.

```vibe
def extents(scores, words)
  {
    lowest: scores.min,
    highest: scores.max,
    bounds: scores.minmax,
    shortest: words.min_by { |w| w.length },
    longest: words.max_by { |w| w.length }
  }
end
```

## Set-like Operations

Use `+` to concatenate and `-` to subtract values:

```vibe
def unique_participants(core, late)
  (core + late).uniq.compact
end

def without_dropouts(participants, dropouts)
  participants - dropouts
end
```

The method forms `union(*others)` and `difference(*others)` accept any number of
array arguments:

- `union` concatenates the receiver with every argument array and removes
  duplicates, keeping the first occurrence of each value. Calling it with no
  arguments deduplicates the receiver. Equality follows the same value semantics
  as `uniq`, so nested arrays and hashes compare by content.
- `difference` returns the receiver's elements that do not appear in any
  argument array. Unlike `union`, it preserves duplicates within the receiver;
  only values found in the arguments are dropped.

```vibe
def attendees(core, late, extra)
  core.union(late, extra)
end

def remaining(roster, departed)
  roster.difference(departed)
end
```

```vibe
[1, 2].union([2, 3], [3, 4]) # => [1, 2, 3, 4]
[1, 1, 2, 3].difference([2])  # => [1, 1, 3]
```

Both methods return a new array and leave the receiver unchanged. A non-array
argument raises an error.

See `examples/arrays/` for concrete scripts exercised by the test suite.
