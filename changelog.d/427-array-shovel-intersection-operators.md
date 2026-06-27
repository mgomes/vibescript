- **Added: Ruby-style array `<<` (shovel) and `&` (intersection) operators.**
  `array << value` appends a single value, returning a new array because
  Vibescript arrays are immutable (`[1, 2] << 3` is `[1, 2, 3]`); accumulate by
  reassigning, `values = values << value`, which reuses the same backing-buffer
  fast path as `push` and `+`. `array & other` returns the elements common to
  both arrays with duplicates removed and the left array's order preserved
  (`[1, 1, 2, 3] & [1, 3, 4]` is `[1, 3]`). Following Ruby, `+` binds tighter
  than `<<`, which binds tighter than `&`, and a spaced `&` is the intersection
  operator while a flush `&block` is still reported as an unsupported block
  pass. Both operators require array operands and the reduce shorthand accepts
  `"<<"` and `"&"`.
