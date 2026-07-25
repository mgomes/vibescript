- **Performance: transforming and filtering a script-built hash no longer
  degrades under a memory quota.** `transform_values`, `select`, and `reject`
  inserted into their result between block calls, which re-measured the whole
  receiver on every check and made the walk grow quadratically with the entry
  count. They now build the result after the loop. Transforming a 600-entry hash
  this way is roughly 24x faster, and it holds slightly less memory while
  iterating.
