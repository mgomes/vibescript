- **Fixed: refining a witnessed hash shape no longer costs the square of the
  writes.** A field write refines the receiver's fact by copying it, so the copy
  every other holder depends on grew with the shape: 2,000 `h[:kN] = 1` writes
  against a growing literal allocated 520MB inside `CheckWarnings`, where no
  script step or memory quota can meter it, and 800 writes of one key against a
  literal whose other field is an 800-field shape allocated 97MB, since counting
  fields rather than the nodes each copy walks missed the second entirely. A
  budget spent by the copies bounds both, and it travels with the fact, so a
  fresh literal elsewhere starts with all of it. The same pairs allocate 11.8MB
  and 5.4MB now. Past the budget the fact gives up claiming to name every key
  and never a key it already names: a shape is authoritative about the keys it
  omits, so dropping one would make the checker decide branches it should leave
  alone.
