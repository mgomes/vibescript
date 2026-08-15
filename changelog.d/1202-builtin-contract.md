- **Fixed: a block body calling a builtin is no longer quadratic in its
  receiver under a memory quota.** `rows.map { |r| r.to_s }` re-walked the whole
  receiver once per element, because builtin dispatch invalidated the memory
  estimator's memo and a memory check inside a builtin bypassed it.
- **Added: `vibes.DeclareNonMutating` and `vibes.DeclareNonRetaining`.** A host
  builtin can declare that it writes to nothing reachable, or retains nothing it
  is handed, and the runtime stops paying for the assumption that it might.
  Both are safety promises rather than performance hints: an untrue one lets an
  execution allocate past its `MemoryQuotaBytes`. Undeclared builtins are
  unaffected.
