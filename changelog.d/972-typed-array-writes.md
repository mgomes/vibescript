- **Improved: the checker reports incompatible element writes to typed
  arrays.** A local known to be `array<T>` now checks shovel appends, indexed
  assignment, and the in-place mutators (`push`, `append`, `prepend`,
  `unshift`, `insert`, `fill`) against `T`, so a provably incompatible element
  is reported at the write instead of silently corrupting a checked boundary.
  Exact `fill` value, selector, padding, and block outcomes participate in the
  same check. Compatible writes preserve the known element type — including
  through aliases, loops, and blocks — while unknown values, unknown
  receivers, unresolved outcomes, and `array<any>` stay gradual.
