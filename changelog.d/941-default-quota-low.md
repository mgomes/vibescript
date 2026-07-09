- **Changed: the zero-value `Config` quota default is now the `low` profile**
  (1,000,000 steps / 16 MiB / 256 recursion) instead of the previous
  50,000 steps / 64 KiB / 64. An embedder that sets no quotas now gets the
  lowest named profile as its budget, so `low` is the reproducible name for the
  default embedding sandbox. Hosts that relied on the previous tighter default
  should set the quota fields (or lower explicit values) explicitly.
