- **Added: Ruby-style `Object#itself`.** `itself` is now available on every
  value kind, including scalars, collections, ranges, temporal values, `nil`,
  and script instances, and returns the receiver unchanged so it preserves
  value ownership and host-boundary isolation. It is handy as an identity step
  in pipelines and block callbacks; it takes no arguments and rejects any
  positional or keyword argument.
