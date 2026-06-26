- **Fixed: rest destructuring now charges its captured window and live
  right-hand side against the memory quota.** When a destructuring assignment
  builds a named rest array (for example `values[1], *rest = values` or
  `a, *rest = build_large_array()`), the captured window is now metered before
  it is allocated, alongside both the right-hand-side snapshot it may coexist
  with and the evaluated right-hand side itself when that value is held only on
  the call stack (a function or capability return, or an array literal). A
  sandboxed script can no longer exceed `MemoryQuotaBytes` by roughly the size
  of the right-hand side by routing a large off-stack array through a rest
  target.
