- **Fixed: a small result of a large string no longer holds the whole string.**
  A Go substring shares its source's backing allocation, so a member returning a
  window onto its receiver kept the entire receiver alive while the memory quota
  priced the window by its own length. Keeping 200 one-character results of a
  megabyte each held 192.2 MiB under an 8 MiB quota. `strip`, `lstrip`, `rstrip`
  and their `!` forms, `chomp(sep)` and `chomp("")` and theirs, `delete_prefix`,
  `delete_suffix` and theirs, the parts of `split`, the lines of `lines` and
  `each_line`, and the matches of `scan` now copy what they return, so a string's
  footprint equals its length. `byteslice`, `slice`, bracket reads and
  `partition` were already copied. A result that spans its whole receiver is
  still handed back untouched and costs nothing. Argumentless `chomp`, `chop` and
  `chop!` deliberately still return a window: they remove at most a couple of
  bytes, so reaching a meaningful gap costs one call per byte — which the step
  quota already prices — while copying would make two constant-cost methods scale
  with the receiver.
- **Changed: these members now allocate their result, which a tight memory quota
  can notice.** The bytes were always charged; they are now actually used, and
  they are reserved before they are allocated, so a call that cannot fit its
  result is rejected before making it rather than after. Trimming a 64 KiB string
  that is almost entirely padding takes 6.6µs where it took 1.7µs, splitting a
  60 KB document into 10,000 fields 6.6% longer, and `lines` over 2,000 lines
  12.7%; a receiver with nothing to trim is unchanged. Walking a string with
  `each_line`, or `scan` with a block, costs about 10-12% more when a memory
  quota is configured and is unchanged without one.
