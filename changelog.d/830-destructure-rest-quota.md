- **Fixed: rest destructuring now charges its captured window against the
  memory quota.** When a destructuring assignment builds a named rest array
  (for example `values[1], *rest = values`), the captured window is now metered
  before it is allocated, alongside the right-hand-side snapshot it coexists
  with. A sandboxed script can no longer exceed `MemoryQuotaBytes` by roughly
  the size of the right-hand side by routing a large array through a rest
  target.
