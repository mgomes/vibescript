- **Performance: function returns no longer heap-allocate.** The non-local
  return check ran on every function return through `errors.As`, whose
  address-of-local argument escaped to the heap — one allocation per return even
  on the common path where no block return was in flight. It now fast-paths a
  nil error and an unwrapped signal without allocating, cutting roughly a third
  of the allocations on deep-recursion workloads.
