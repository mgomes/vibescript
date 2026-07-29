- **Fixed: array scans charge the step quota per element.** `include?`, `index`,
  `rindex`, `min`, `max`, `minmax`, and the blockless `uniq` scanned the whole
  receiver for a flat handful of steps, so a script could scan an arbitrarily
  large host-supplied array on a constant budget. They now charge one step per
  element, as `sum` and `reverse` already did. An early match still exits early
  and costs only the elements it examined. `uniq` in both forms additionally
  charges for the equality probes a composite costs, since deduplicating
  composites compares each one against every distinct composite already seen.
