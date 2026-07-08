- **Performance: script-function calls reuse their argument backing.** Each call
  evaluated its positional arguments into a freshly allocated slice. Calls to
  script functions now borrow the backing from a per-execution free list and
  return it once the call unwinds — safe because argument binding copies every
  value into the callee's environment and never retains the slice. Combined with
  the non-local-return change, this cuts recursive fib(20) from 65,704 to 21,947
  allocations per call. Calls to builtins and capabilities, which may retain the
  slice, are unaffected.
