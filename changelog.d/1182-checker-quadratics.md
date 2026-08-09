- **Fixed: five static-check paths no longer cost the square of the source.**
  Every one of them runs inside `CheckWarnings` and `CheckedCall`, before any
  script step or memory quota can meter it. Binding a destructured block
  parameter re-derived the same decomposition and rest element for every
  element, so a 4,000-target `(x1, ..., xN, *rest)` inspected 16.0M elements
  against 4,002 now. Modeling a rest parameter rebuilt every aggregate at every
  argument, so `sink(0, 0, ...)` against `def sink(*xs)` allocated 274MB at
  2,000 arguments against 5.5MB now. Return summaries were keyed by a context
  naming every namespace member recorded as possibly reassigned, rebuilt on each
  of the many lookups a statement makes and kept once per call: 800 member
  writes with a call after each allocated 475MB against 68MB now, and a summary
  whose walk does read those members is still separated by all of them.
  Refining a witnessed literal shape now stops at 64 fields (2,000
  `h[:kN] = 1` writes: 520MB against 11.8MB) and one stored block's body is
  walked for its result at most 4,000 statements per check (a 200-statement proc
  filled 200 times: 831MB against 87MB). Past their bound both weaken the fact,
  and the block walk also leaves the call unable to complete, since the walk it
  skipped could have proved the body always raises and cut the code after the
  fill. Both can therefore cost a diagnostic but never produce one.
