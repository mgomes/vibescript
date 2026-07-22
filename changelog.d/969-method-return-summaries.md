- **Improved: unannotated methods expose inferred return summaries.** Calls
  to statically resolved instance and class methods now carry the same
  branch, fallthrough, and cycle-aware summaries as plain functions. Dynamic
  receivers and overrides remain unknown, while explicit return annotations
  stay authoritative.
