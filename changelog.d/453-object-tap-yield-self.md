- **Added: Ruby-style `Object#tap` and `Object#yield_self`.** Every core value
  kind now responds to these block-yielding helpers. `tap` yields the receiver
  and returns the receiver (so block results are discarded), while `yield_self`
  yields the receiver and returns the block's result. Both require a block, take
  no other arguments, and resolve only when the receiver does not already define
  a member of the same name, so a hash key, instance variable, or user-defined
  method named `tap`/`yield_self` keeps precedence.
