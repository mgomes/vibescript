- **Added: Ruby-style `block_given?`.** Functions and methods can now ask
  `block_given?` whether the current call was supplied a block, returning `true`
  when one was given and `false` otherwise, so optional block APIs branch with
  `if block_given?` instead of letting `yield` raise. It is reserved (it cannot
  be shadowed by a local), reports the enclosing method's block when used inside
  a block, and the parenthesized `block_given?()` form takes no arguments.
