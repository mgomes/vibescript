- **Fixed: array comparison was unmetered and exponential on shared graphs.**
  Comparing two arrays that share subtrees did work exponential in their depth
  -- 2.1 seconds at depth 24 -- while charging no steps, so a script could
  monopolize the runtime from inside `<=>` whatever its limits. Completed pairs
  are now memoized and every compared element charges a step. A sandbox limit
  reached during a sort keeps its classification rather than being relabelled as
  an incomparability.
