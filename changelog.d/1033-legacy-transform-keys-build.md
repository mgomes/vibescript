- **Performance: remapping the keys of a host-supplied hash no longer degrades
  under a memory quota.** `transform_keys` inserted into its result between block
  calls when the receiver came from the host rather than from a script, which
  re-measured the whole receiver on every check and made the walk grow
  quadratically with the entry count. Remapping a 1000-entry hash this way is now
  roughly 40x faster.
