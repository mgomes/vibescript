- **Improved: unannotated functions expose inferred return summaries.** Calls
  to a plain script function whose body provably yields known types now carry
  that union — explicit returns, implicit finals, and nil fallthrough
  included — so `takes_string(build_count())` is checked without an
  annotation. Recursive, dynamic, or partially unknown bodies stay unknown,
  and explicit annotations remain authoritative.
