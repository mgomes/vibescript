- **Improved: the checker narrows nullable locals across control flow.**
  Truthiness tests, explicit nil comparisons, and `nil?` now refine a local's
  known type on both branches — including `unless`, `elsif`, negation,
  short-circuits, and guard clauses that exit early — so nil misuse inside a
  guarded branch is reported and provably dead branches stop warning.
