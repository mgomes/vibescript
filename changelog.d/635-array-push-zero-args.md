- **Fixed: zero-argument `Array#push` returns the array like Ruby.** A bare
  `array.push` now reads as a zero-argument call that returns the array instead
  of leaking the unbound method value, and `array.push()` no longer raises
  "expects at least one argument". This matches Ruby, where the call has no
  parentheses distinction, so `[1, 2].push` and `[1, 2].push()` both return
  `[1, 2]` while `[1, 2].push(3)` still returns `[1, 2, 3]`. A keyword-only
  call such as `[1, 2].push(foo: 1)` now raises rather than silently dropping
  the keyword map, since `Array#push` does not accept keyword arguments.
