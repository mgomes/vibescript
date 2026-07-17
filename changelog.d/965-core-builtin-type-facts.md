- **Improved: the checker knows core builtin signatures.** Conversions, ID,
  money, Math, Duration, Time, and JSON/Regex helpers now declare fixed
  argument and result types, so provably wrong arguments and misused results
  (`takes_string(to_int("1"))`) are reported by `vibes check` instead of
  failing at runtime. Argument-dependent results stay unknown and host
  overrides still disable the default contracts.
