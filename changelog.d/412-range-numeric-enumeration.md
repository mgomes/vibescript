- **Added: Ruby-style numeric and range enumeration helpers.** Integers answer
  `upto(limit)`, `downto(limit)`, and `step(limit, step = 1)`, each yielding the
  matching integer sequence to a block and returning the receiver; `upto` and
  `downto` yield nothing when the receiver is already past the limit, and
  `step`'s nonzero stride selects the direction. Ranges gain the Enumerable
  helpers `each`, `each_with_index`, `map`, `select`, `reject`, `find`, `reduce`,
  `sum`, `count`, and `step(n)`, which walk the range's integer sequence lazily in
  iteration order (descending for ranges such as `5..1`). Every helper charges a
  step per element, and the array-building helpers charge their result against the
  memory quota as it grows, so a large range fails safely on the sandbox limits
  instead of running unbounded or exhausting memory.
