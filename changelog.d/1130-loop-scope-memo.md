- **Performance: a loop under a memory quota no longer re-walks the heap every
  iteration.** A statement list evaluates in the scope it is handed rather than a
  fresh one, so a loop body re-pushes the enclosing scope each iteration. That
  duplicate stack slot was treated as a topology change and invalidated the
  memory estimator's memo twice per iteration, making a loop that only reads
  quadratic in its own iteration count. Reading a 4,000-element array in a
  `while` loop under a 64 MiB quota drops from 63ms to 5ms, and the same loop is
  now linear rather than quadratic.
