- **Added: Ruby-style callable literals, block forwarding, and call splats.**
  `Proc.new { }`, `proc { }`, `lambda { }`, and the stabby lambda
  `->(args) { ... }` (brace or `do ... end` body) build first-class callables
  invoked with `.call`. Procs keep block semantics — padded arguments,
  single-array auto-splat, and non-local `return` that unwinds the defining
  method (raising `LocalJumpError` once that frame is gone) — while lambdas
  enforce strict arity (`lambda expects 2 arguments, got 1`) and treat
  `return`, `break`, and `next` as local to the lambda; `fn.lambda?` reports
  which semantics a callable carries. Ampersand block arguments forward
  callables as a call's block: `m(&blk)` passes a captured block through
  (preserving non-local return across the hop), `&fn` forwards a function or
  bound method with its own arity checking, `&:name` is symbol-to-proc with
  public-only dispatch (private methods raise) and operator symbols routed
  through the arithmetic helpers, and `&nil` means no block. Call splats
  expand prepared argument lists in place — `f(*args)`, `f(**opts)`, and
  combined forms with regular arguments and blocks — before binding, so
  arity, keyword, and type errors match the literal spelling and the expanded
  arguments are charged against the step and memory quotas exactly like
  literal arguments; `tasks.spawn(:name, *args, **opts)` forwarding now works.
  The `-> Type` return annotation on `def` must sit on the signature line; a
  `->` opening the next line parses as a lambda literal.
