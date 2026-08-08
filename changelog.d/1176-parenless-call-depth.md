- **Fixed: a line of many parenless calls no longer crashes the host.** A
  parenless call parses its argument as a full expression, and that argument may
  be another parenless call, so `a a a a ...` on one line nests one call per
  identifier. Half a megabyte of them fits well inside the default source-size
  limit and drove the parser deep enough to overflow the goroutine stack, which
  is fatal for the whole process rather than for the script, and long before
  that the parse was already taking seconds. Parenless calls now nest at most 64
  deep, the same bound type annotations have carried, and a line past it reports
  "parenless call nesting too deep" against the argument that would have opened
  the next level. Real code stacks two or three deep (`puts format value`), so
  nothing written on purpose comes near the cap.
