- **Added: Ruby-style `Array#each_slice`, `each_cons`, `reverse_each`, and
  `cycle`.** `each_slice(n)` yields non-overlapping slices (including a shorter
  trailing slice) and `each_cons(n)` yields sliding windows; both require a
  positive integer size and yield freshly copied arrays that do not alias the
  receiver. `reverse_each` yields values in reverse index order and returns the
  receiver. `cycle(n)` repeats the array `n` times (a non-positive count is a
  no-op like Ruby), while omitting the count or passing `nil` cycles forever; the
  cycle charges a step per yield so the step quota and context cancellation bound
  even an empty block body. The slice/window/cycle forms return `nil` to match
  Ruby.
