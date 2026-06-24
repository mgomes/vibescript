- **Hardened interpolated string materialization under sandbox limits.**
  Double-quoted interpolation now builds its result incrementally, charging a
  step and checking the projected byte length against the memory quota before
  appending each segment. The projection for an interpolated expression is
  computed without rendering the value, so an aggregate whose representation
  expands far beyond its own footprint (for example an array or hash holding
  many references to one large string) is rejected before the oversized join is
  materialized rather than after. A script that grows a string through repeated
  or large interpolation (for example `"#{text}#{text}"` in a loop) now fails
  with a memory quota error before the oversized result is materialized, and a
  canceled context stops construction promptly. Small interpolations are
  unchanged.
