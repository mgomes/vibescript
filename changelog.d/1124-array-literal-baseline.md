- **Faster: building a one-element array no longer walks the whole reachable
  graph.** Every array literal opened an incremental build accumulator whose
  baseline is an unmemoized reference walk, so allocating a single slot cost
  O(reachable). A script nesting a structure in a loop paid that on every
  iteration. A one-element literal now charges its result once through the
  memoized check instead, which measures 1.7x faster on a 2000-deep build under
  a quota.
