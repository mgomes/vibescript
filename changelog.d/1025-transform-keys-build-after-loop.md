- **Performance: remapping the keys of a script-built hash no longer degrades
  under a memory quota.** `transform_keys` inserted into its result between block
  calls, which re-measured the whole receiver on every check and made the walk
  grow quadratically with the entry count. It now builds the result after the
  loop. Remapping a 600-entry hash this way is roughly 24x faster.
