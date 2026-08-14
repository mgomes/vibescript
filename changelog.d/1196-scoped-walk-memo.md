- **Fixed: another script running at the same time can no longer make this one's
  memory accounting quadratic.** The memory-quota estimator memoizes its
  reachable-graph walk, and the memo was keyed on a counter shared by the whole
  process, so any execution's write discarded every memo and each miss re-walked
  its victim's entire reachable graph. A second script doing small writes drove an
  unrelated one's estimator work from linear to quadratic in its own receiver --
  across separate `Engine`s, sharing no value -- and because builtin dispatch also
  advances that counter, a loop of pure builtin calls did it while mutating
  nothing at all. The walk is deliberately uncharged, so `StepQuota` never
  intervened. The memo key is now a pair: a counter private to each execution and
  the process-wide one, with writes attributed to an execution advancing only its
  own. Measured as estimator nodes per receiver element under a competing
  execution, across twelve categories of script-visible write, amplification over
  running alone falls from between 19x and 513x to single digits. Scripts running
  alone are unaffected. Two shapes still advance the process-wide counter because
  they have no execution in scope (the legacy string-map materialization behind
  `Hash#keys`, and the bound-receiver cell); both measure in the same single-digit
  band, so neither is an amplification vector.
  `VIBES_ESTIMATOR_VERIFY` checks every memoized total against a from-scratch
  reference walk.
- **Known: a block whose body calls any builtin is still quadratic in its
  receiver.** `a.map { |x| x.to_s }` walks the estimator's graph once per element
  rather than once per loop, because builtin dispatch invalidates the
  block-iteration region's prefix memo on every call. This is a single execution
  with no concurrency and no mutation; estimator nodes per element are 1019 /
  2019 / 4019 at n = 1000 / 2000 / 4000, against a flat 13.1 for a block body that
  calls no builtin. The scoping above does not address it -- the invalidating bump
  is the script's own, so scoping correctly leaves it invalidating -- and removing
  it needs a way to know that a builtin does not mutate, which is left to its own
  change.
