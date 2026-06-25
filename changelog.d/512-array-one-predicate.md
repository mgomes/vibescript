- **Added: Ruby-style `Array#one?`.** `one?` is true only when exactly one
  element is truthy, or with a block, when exactly one block result is truthy.
  It stops scanning once a second match is found and respects the existing step
  quota and cancellation checks during block iteration.
