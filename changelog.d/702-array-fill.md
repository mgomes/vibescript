- **Added: Ruby-style `Array#fill`.** `fill` replaces all or part of an array
  with a value (`fill(value)`, `fill(value, start, length)`, `fill(value,
  range)`) or with values computed from each index by a block (`fill { |i|
  ... }`, optionally narrowed by a `start`/`length` or range). It follows Ruby's
  indexing rules: a negative `start` counts back from the end, a `length` or
  range that runs past the end grows the result and pads any gap with `nil`, a
  `nil` `start` is read as `0` and a `nil` `length` as omitted (filling to the
  end), and the value and block forms are mutually exclusive. Like the neighboring array
  helpers, `fill` returns a new array and leaves the receiver untouched, and it
  builds the result under the sandbox step and memory quotas so a growth larger
  than the limits fails safely instead of exhausting memory.
