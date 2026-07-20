- **Added: `vibes.CheckWarning` is the public checker diagnostic type.** The
  `CheckWarnings*` family already returned these values, but embedders could
  not name the type outside inference. The stable alias carries the function,
  source position, message, and originating module path.
