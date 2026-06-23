- **Hardened `for`-loop sandbox accounting.** Each `for` iteration over an array
  or range now charges a step before evaluating the body, matching `while` and
  `until`. A large `for` loop therefore still respects the step quota and still
  surfaces `context.Canceled` once a host callback cancels the context, even when
  the loop body is empty.
