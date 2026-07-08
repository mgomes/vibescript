- **Changed: `vibes run`, `vibes test`, and the REPL now default to the
  `xhigh` quota profile** — unlimited steps and memory with a high finite
  recursion cap — so CPU-heavy scripts (deep recursion, long loops) run out of
  the box instead of tripping the embedding sandbox's small default quotas. Pass
  `-profile low|medium|high|xhigh` to simulate a constrained sandbox budget, or
  `-step-quota` / `-memory-quota` / `-recursion-limit` (with `-1` for unlimited)
  to override an individual quota on top of the selected profile.
