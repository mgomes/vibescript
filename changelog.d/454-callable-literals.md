- **Added: Ruby-style callable literals, block forwarding, and call splats.**
  `Proc.new { }`, `proc { }`, `lambda { }`, and the stabby lambda
  `->(args) { ... }` (brace or `do ... end` body) build first-class callables
  invoked with `.call`. Procs keep block semantics — padded arguments,
  single-array auto-splat, and non-local `return` that unwinds the defining
  method (raising `unexpected return`, a rescuable `LocalJumpError`, once
  that frame is gone) — while lambdas
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
- **Changed: parenless `callee *expr` / `callee **expr` is now a splat call,
  and the `-> Type` annotation must sit on the signature line.** Following
  Ruby's spacing rule, `f *n` and `f **n` — a space before the star, none
  after — where the callee is a zero-arg function or member call now parse as
  a call with a splat argument (raising `splat argument must be an array` /
  `keyword splat argument must be a hash` when the operand is not one)
  instead of multiplying or exponentiating the call's value. To keep the
  arithmetic reading, call explicitly (`f() * n`, `b.size() * n`),
  parenthesize the callee (`(f) * n`), or space the operator on both sides
  (`f * n`, `b.size * n`) — all of which evaluate exactly as before. Local
  variables are unaffected: `x *n` is still multiplication. Relatedly, the
  `-> Type` return annotation on `def` must now sit on the signature line: a
  `->` opening the following line parses as a stabby lambda literal instead
  of the previous line's return annotation.
