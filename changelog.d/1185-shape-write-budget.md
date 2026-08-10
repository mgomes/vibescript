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
  and never a key it already names. The asymmetry is what matters: the checker
  rules a branch out from the type of a field the fact names, so a fact that
  stopped naming one would stop ruling that branch out, while the claim to name
  them all decides nothing by itself and costs nothing to give up.
- **Changed: a hash built from a very wide literal may now report mistakes in
  branches the checker used to prove unreachable.** The budget above stops
  refining once a script overwrites a few hundred fields of a literal that names
  a few hundred, with values of a type the literal did not give them; a hundred
  such overwrites is still well inside it, and smaller scripts never reach it at
  all. Past that point the checker gives the hash's shape up rather than keep a
  partial one, so a condition reading one of its fields stops being decided and
  the arm that condition used to rule out is checked like any other. What turns
  up there is real: a mistyped call or an undefined name that would have failed
  had the arm ever run. It is never a complaint about correct code, since an arm
  with nothing wrong in it stays silent, and code that runs either way is
  unaffected, because a field the checker has stopped tracking reads as unknown
  rather than as something else. Keeping the shape instead would have meant
  copying it once per overwrite, which is the cost this fix exists to remove.
