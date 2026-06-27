- **Added: Ruby-style range and numeric enumeration helpers.** Integers now
  answer `upto(limit)`, `downto(limit)`, and `step(limit, by = 1)`, each yielding
  the relevant integers to the block and returning the receiver. Ranges gained
  Enumerable-style iteration helpers `each`, `step(n)`, `map`, `select`,
  `reject`, `find`, `reduce`, `count`, `sum`, `min`, and `max`. The iteration
  follows Vibescript's range direction (ascending for `1..5`, descending for
  `5..1`) and charges one sandbox step per element so a wide span fails on the
  step quota rather than running unbounded; the array-building helpers (`map`,
  `select`, `reject`) honor the memory quota as the result grows. `step` advances
  by its stride directly, so a sparse step over a wide span only charges the step
  quota for the values it yields. `step` arguments must be nonzero (integers) and
  positive (ranges), `sum` errors on 64-bit overflow, and the terminal value is
  detected before each increment so a span reaching the 64-bit bounds stops
  cleanly instead of wrapping.
