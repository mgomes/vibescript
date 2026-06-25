- **Added: Ruby-style `Array#values_at`.** `values_at(*selectors)` reads several
  elements at once, returning a new array in the order the selectors were
  requested. An integer selector reads one element: negative indexes count back
  from the end, out-of-bounds indexes yield `nil`, and duplicate indexes repeat
  their values. A range selector reads a window and flattens its elements into
  the result in place, so `values_at(0..1)` is `[a[0], a[1]]` and integer and
  range selectors can be interleaved (`values_at(0..1, -1)`); a range whose end
  extends past the array pads the missing positions with `nil`, while a range
  whose negative start counts back before the beginning raises. Float indexes and
  float range bounds truncate toward zero like Ruby's `to_int` conversion;
  non-numeric selectors and keyword arguments raise.
