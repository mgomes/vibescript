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
- `filter_map` to transform elements and keep only the truthy results in one
  pass, dropping falsy block returns (the fused equivalent of `map` then a
  truthiness filter).
- `select` to keep items the block accepts.
- `reject` to keep items the block rejects (the inverse of `select`).
- `find` to locate the first matching item.
- `find_index(value)` / `find_index { ... }` to locate the first matching index.
- `reduce` to accumulate values, either with a block or with a symbol/string
  operation shorthand (`[1, 2, 3].reduce("+")`, `["a", "b"].reduce(:concat)`).
- `first` / `last` to read an end element, or `first(n)` / `last(n)` to slice without mutating. The optional count is the only argument they accept; passing more than one positional argument or any keyword argument raises.
- `take(n)` / `drop(n)` to keep or skip a prefix; both reject negative counts.
- `zip(*arrays)` to combine arrays element-wise into rows, padding short arrays with `nil`.
- `transpose` to swap the rows and columns of a matrix of equal-length array rows; it raises when a row is not an array or the rows differ in length.
- `push`/`pop` for building or removing values while keeping the original array untouched.
- `sum` to total numeric arrays.
- `compact` to drop `nil` entries.
- `flatten(depth = nil)` to collapse nested arrays. No argument, `nil`, or a negative depth flattens fully; `0` returns a shallow copy; a positive depth flattens that many levels and a `Float` depth is truncated to an integer. A nonnumeric depth raises.
- `fill(value)` / `fill(value, start, length)` / `fill(value, range)` to replace all or part of an array with a value, returning a new array. A block form `fill { |index| ... }`, optionally narrowed by a `start`/`length` or range (`fill(start) { ... }`, `fill(start, length) { ... }`, `fill(range) { ... }`), computes each replacement from its index. When a block is given there is no fill-value argument: every positional argument selects the window, so `fill(0) { |i| ... }` fills from index `0` to the end rather than filling with `0`.
- `chunk(size)` to split into fixed-size slices.
- `window(size)` to build overlapping windows.
- `join(sep = "")` to produce a string. Nested arrays are joined recursively with the same separator, so `[1, [2, 3], 4].join("-")` is `"1-2-3-4"`; `nil` elements contribute an empty segment (`[1, nil, "x"].join(",")` is `"1,,x"`); and an empty array joins to `""`. The separator must be a string.

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
[1, 2, 3, 4].filter_map { |n| if n % 2 == 0 then n * 10 end }   # [20, 40]
```

`filter_map` requires a block and takes no arguments. It calls the block once
per element and collects each result the block returns, dropping any falsy
result. Like `select` and `reject`, it uses Vibescript's truthiness, so `nil`,
`false`, `0`, `""`, and empty collections are all dropped; only truthy results
survive.

```vibe
[1, 2, 3, 4, 5].chunk(2)   # [[1,2], [3,4], [5]]
[1, 2, 3, 4].window(3)      # [[1,2,3], [2,3,4]]
[1, 2, 3].take(2)           # [1, 2]
[1, 2, 3].drop(1)           # [2, 3]
[1, 2].zip([3, 4], [5])     # [[1, 3, 5], [2, 4, nil]]
[[1, 2], [3, 4]].transpose  # [[1, 3], [2, 4]]
[1, 2, 3].fill(0)           # [0, 0, 0]
[1, 2, 3].fill(0, 1, 2)     # [1, 0, 0]
[1, 2, 3].fill("x", 1..2)   # [1, "x", "x"]
[1, 2, 3].fill { |i| i * 10 }    # [0, 10, 20]
[1, 2, 3].fill(0) { |i| i * 10 } # [0, 10, 20] (0 is the start index, not a value)
```

`fill` follows Ruby's indexing rules: a negative `start` counts back from the
end, a `length` that runs past the end grows the result (padding any gap with
`nil`), and a range selects the indices to replace. An explicit `length` of `0`
whose `start` is past the end still grows the array up to that start, padding the
gap with `nil` even though nothing is filled (`[1, 2, 3].fill(0, 5, 0)` is
`[1, 2, 3, nil, nil]`); a negative `length`, by contrast, is a pure no-op that
never grows the array. A `nil` `start` is read as `0` and a `nil` `length` as
omitted (filling to the end), so optional selectors held in variables that
default to `nil` behave like Ruby (`[1, 2, 3].fill(0, nil)` is `[0, 0, 0]`,
`[1, 2, 3].fill(0, 1, nil)` is `[1, 0, 0]`). The value form and the block
form are mutually exclusive: a block is never consulted when an explicit fill
value is given, and when a block is given there is no fill-value argument at all.
Every positional argument passed alongside a block selects the window, so
`fill(0) { |i| ... }` fills from index `0` to the end with block results rather
than filling with `0`. Like the other array helpers, `fill` returns a new array
and leaves the receiver untouched.

## Search and predicates

- `include?(value)` for membership checks.
- `index(value)` / `index { ... }` returns the first matching index, or `nil` on
  a miss. The block form returns the first index whose block result is truthy.
  `find_index` is an alias with the same value and block forms.
- `rindex(value)` / `rindex { ... }` returns the last matching index, scanning
  from the end, or `nil` on a miss.
- `count`, `count(value)`, or `count { ... }`. As in Ruby, a `value` argument
  takes precedence: `count(value) { ... }` counts elements equal to `value` and
  ignores the block.
- `any?`, `all?`, `none?` with optional blocks.

`index`, `find_index`, and `rindex` accept either a value or a block, never both;
passing both raises an error. As a Vibescript extension, the value form also takes
an optional non-negative offset to start (`index`/`find_index`) or cap (`rindex`)
the search: `index(value, offset)` / `rindex(value, offset)`.

```vibe
def health_checks(values)
  {
    has_zero: values.include?(0),
    first_large_idx: values.index(100),
    first_negative_idx: values.index { |v| v < 0 },
    last_negative_idx: values.rindex { |v| v < 0 },
    all_non_negative: values.all? { |v| v >= 0 }
  }
end
```

## Indexed access

- `at(index)` returns the single element at `index`, counting a negative index
  back from the end. An out-of-range index returns `nil` rather than raising, so
  it never goes out of bounds the way bracket access does. It agrees with
  `[index]` for every in-range non-negative index.
- `slice(index)` mirrors `at(index)`, returning the single element (or `nil`
  out of range).
- `slice(start, length)` returns a new subarray of up to `length` elements
  starting at `start`. A negative `start` counts back from the end. A `start`
  exactly equal to the length with a non-negative `length` yields `[]`, while a
  `start` past the length or a negative `length` returns `nil`. An oversized
  `length` is clamped to the remaining elements.
- `slice(range)` returns a new subarray selected by the range bounds, aligning
  with the range slicing already available for strings. Negative bounds count
  back from the end, an exclusive range drops its end, an end before begin yields
  `[]`, and a begin past the length returns `nil`.

Indexes and lengths accept `Float` values, which are truncated toward zero like
Ruby's `to_int`; any other type raises. The subarray forms always return a fresh
copy, so mutating the result never touches the original array.

```vibe
[10, 20, 30].at(-1)         # 30
[10, 20, 30].at(9)          # nil
[10, 20, 30, 40].slice(1, 2) # [20, 30]
[10, 20, 30].slice(3, 1)    # [] (start at the length)
[10, 20, 30].slice(4, 0)    # nil (start past the length)
[1, 2, 3, 4].slice(1..2)    # [2, 3]
[1, 2, 3, 4].slice(-3..-1)  # [2, 3, 4]
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
