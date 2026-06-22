- **Added: Ruby-style `call` on function values.** A function value now exposes
  a `call` member so `fn.call(...)` mirrors direct `fn(...)` invocation,
  forwarding positional arguments, keyword arguments, and an optional block.
  Arity and type errors stay anchored at the call site, and `call` is the only
  member offered (with a "did you mean" hint for typos).
