- **Fixed: a block body calling a builtin is no longer quadratic in its
  receiver under a memory quota.** `rows.map { |r| r.to_s }` re-walked the whole
  receiver once per element, because builtin dispatch invalidated the memory
  estimator's memo and a memory check inside a builtin bypassed it.
- **Added: `vibes.DeclareNonMutating`.** A host builtin can declare that it
  writes to nothing reachable from its receiver, arguments, keyword arguments,
  block, or an execution's roots, and the runtime stops paying for the
  assumption that it might. This is a safety promise rather than a performance
  hint: an untrue one lets an execution allocate past its `MemoryQuotaBytes`.
  Undeclared builtins are unaffected.
- **Added: `vibes.DeclareNonRetaining`.** The companion promise, that a builtin
  keeps no reference to anything it is handed or returns. It is recorded but not
  yet consulted, so declaring it changes no behavior today; it is published
  ahead of the change that reads it.
