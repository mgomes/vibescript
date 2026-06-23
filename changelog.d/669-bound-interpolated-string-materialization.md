- **Hardened interpolated string materialization under sandbox limits.**
  Double-quoted interpolation now builds its result incrementally, charging a
  step and checking the projected byte length against the memory quota before
  appending each segment. A script that grows a string through repeated or
  large interpolation (for example `"#{text}#{text}"` in a loop) now fails with
  a memory quota error before the oversized result is materialized, and a
  canceled context stops construction promptly. Small interpolations are
  unchanged.
