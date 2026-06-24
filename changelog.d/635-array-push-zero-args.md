- **Fixed: zero-argument `Array#push` returns the array like Ruby.** A bare
  `array.push` now reads as a zero-argument call that returns the array instead
  of leaking the unbound method value, and `array.push()` no longer raises
  "expects at least one argument". This matches Ruby, where the call has no
  parentheses distinction, so `[1, 2].push` and `[1, 2].push()` both return
  `[1, 2]` while `[1, 2].push(3)` still returns `[1, 2, 3]`.
