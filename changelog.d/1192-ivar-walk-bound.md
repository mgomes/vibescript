- **Fixed: checking a lambda that calls itself many times no longer costs the
  square of its body.** A body reachable from itself is walked twice for the
  instance variables it may write — once under the caller's facts, once under
  the state the recursive call left behind — but the bound on those walks was
  restored at every recursive call site rather than spent for the walk as a
  whole, so each site started its own walk over the whole body. A body holding
  3,200 recursive calls, well inside the source-size limit, took 12.7s inside
  `CheckWarnings`, where no script step or memory quota can meter it; it takes
  37ms now, and 800 calls allocate 1.7MB against 109MB. Both walks still run, so
  no diagnostic changes: recursion that writes no instance variable keeps its
  exact facts, and a write the recursion enables is still collected.
