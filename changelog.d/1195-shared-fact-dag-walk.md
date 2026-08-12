- **Fixed: comparing two type facts that share their nested values no longer
  takes exponential time.** A fact built by repeatedly nesting a value under
  more than one key — `x = { a: x, b: x }` — has as many paths through it as it
  has combinations of keys, but only as many distinct values as lines that built
  it. The exact comparison the checker uses to decide whether two facts are the
  same walked those paths rather than those values, so comparing two
  independently built copies of a 20-line fact of that shape took two million
  comparisons and every further line doubled it, hanging `CheckWarnings` on a
  script well inside the source-size limit. It now remembers the pairs it has
  already proved equal, which reaches each pair once: the same comparison takes
  68. Scripts that do not nest a value under more than one key are unaffected and
  allocate nothing extra.
