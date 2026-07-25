- **Performance: accumulating into a local while iterating no longer degrades
  under a memory quota.** Summing or counting into a variable from inside a
  block (`total = total + x`) re-measured the whole receiver collection on every
  iteration, so the loop cost grew quadratically with the collection size.
  Iterating 600 rows this way is now roughly 48x faster.
