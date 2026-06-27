- **Added: Ruby-style array `<<` (shovel) and `&` (intersection) operators.**
  `array << value` appends a single value, returning a new array because
  Vibescript arrays are immutable (`[1, 2] << 3` is `[1, 2, 3]`); accumulate by
  reassigning, `values = values << value`, which reuses the same backing-buffer
  fast path as `push` and `+`. `array & other` returns the elements common to
  both arrays with duplicates removed and the left array's order preserved
  (`[1, 1, 2, 3] & [1, 3, 4]` is `[1, 3]`). Following Ruby, `+` binds tighter
  than `<<`, which binds tighter than `&`. Mirroring Ruby's spacing rule, only an
  `&` detached from the callee yet flush against its operand (`call &block`) is
  reported as an unsupported block pass; every other shape is the intersection
  operator, including the spaced `items & others`, the flush `items&others`, and
  a trailing `&` line continuation. Both operators require array operands and the
  reduce shorthand accepts `"<<"` and `"&"`.
