- **Fixed: string character-set scans are now charged for the entries they
  compare.** `count`, `delete`, `tr` and `squeeze` charged one step per receiver
  character but compared that character against every entry of every character
  set, so a large non-matching set bought the product of both arguments for the
  price of the receiver alone: over a fixed 100 KB receiver, growing the set
  from 1 entry to 8192 took `count` from 1.8ms to 750ms and `tr` from 1.6ms to
  5.9s while both charged exactly 100,000 steps either way. Every lookup now
  bills the entries it compares, in the output pass as well as the sizing pass,
  carrying the sub-step remainder so ordinary short sets still cost nothing.
