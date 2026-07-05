- **Added: check mode flags unresolved value and function names.** `vibes run
  -check` and the `CheckWarnings*` APIs now report `undefined variable NAME`
  for bare value/function references that cannot resolve to any binding the
  checker can see, matching the error the reference raises at runtime. The
  resolution model deliberately over-approximates the defined-name set —
  locals any branch can bind, exports of any statically resolvable `require`
  anywhere in the script, builtins, host globals and capabilities supplied
  through `CallOptions`, and implicit-self scopes in methods and class bodies
  — so a warning means the reference is guaranteed to fail. A `require` whose
  module name is not a literal suppresses the check for the whole script.
  `-e` snippets additionally run an order-independent whole-snippet pass so
  functions the snippet never calls are covered. Hosts that inject globals or
  capabilities must check with the same `CallOptions` they later pass to
  `Call`; those names then resolve and are never reported. `vibes analyze`
  deliberately does not surface these warnings: it runs over documentation
  fragments and host-embedded scripts whose free names are legal at runtime.
- **Added: check mode rejects typed block parameters contradicted by literal
  receivers.** When a literal array of scalar literals flows through a builtin
  element iterator (`each`, `map`, `select`, `reject`, `find`, and
  `each_with_index` with its integer index parameter), a block parameter type
  annotation that any yielded element misses is now a check warning —
  `argument NAME expected TYPE, got KIND` at the annotation, exactly the error
  the first mismatching yield raises at runtime. The check stays silent for
  non-literal or empty receivers, hash receivers, destructured or extra block
  parameters, untyped parameters, user-defined iterators, and iterators
  outside the covered set, leaving those shapes to runtime enforcement.
