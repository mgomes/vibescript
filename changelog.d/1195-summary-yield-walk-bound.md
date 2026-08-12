- **Fixed: checking a function whose lambda calls itself many times no longer
  costs the square of the lambda's body.** A lambda the checker can prove runs
  is re-checked so a yield it reaches can withdraw the enclosing function's
  inferred return type, and that re-check re-enters the body at every recursive
  call site, because a yield only a later site enables is reachable only from
  there. Nothing bounded how many of those re-entries one summary could run, so
  a body holding 320 recursive calls — well inside the source-size limit — took
  4m14s inside `CheckWarnings`, where no script step or memory quota can meter
  it. It takes 1.5s now, and the statements those walks visit fall from 206,082
  to 1,284. A re-entry that reaches a yield makes every later one a provable
  no-op, so nothing changes for a body whose yield can run. Past eight re-entries
  that reach none, the checker stops trying to prove the yield unreachable and
  withdraws the function's inferred return type instead — the same answer it
  already gives for a lambda it cannot resolve — so a script with nine or more
  recursive calls to one lambda may lose a diagnostic about that function's
  result. None gains one.
