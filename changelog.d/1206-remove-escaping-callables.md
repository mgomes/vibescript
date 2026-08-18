- **Removed: escaping callables; blocks are enforced-nonescaping.** Proc and
  lambda constructors, stabby-lambda literals, first-class function and
  bound-method values, callable `.call`, block capture and forwarding with
  `&`, symbol-to-proc, and hash default procs are gone; each spelling is a
  compile error naming the replacement. A block stays syntax attached to a
  call, runs synchronously under `yield`, and is retired when the receiving
  call returns, so a late invocation -- however the block was retained -- is
  a hard runtime error rather than a documented host contract. Migrate a
  callable value to a named function called where it is needed, or a block
  written at the call that runs it: `words.map(&:upcase)` becomes
  `words.map { |word| word.upcase }`. (#1206)
