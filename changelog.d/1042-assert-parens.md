- **Fixed: `assert(cond, "msg")` was a parse error.** `assert` was parsed as a
  bespoke statement form, so the parenthesised call read `(cond, "msg")` as a
  grouped expression and failed on the comma, despite the documented two-argument
  signature. It now parses as an ordinary call. The paren-less forms, including
  `assert (a > b), "msg"` where the space makes the parentheses a grouped first
  argument, are unchanged.
