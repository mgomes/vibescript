- **Added: Ruby-style operator and index method definitions.** Classes can now
  define `+`, `-`, `*`, `/`, `%`, `**`, `<<`, `&`, `==`, `!=`, `<`, `<=`, `>`,
  `>=`, `<=>`, `[]`, and `[]=` as instance methods, and operator, indexing, and
  index-assignment syntax dispatches to them on the receiver. A user `==`
  defines both `==` and (absent an explicit method) `!=`; compound index
  assignment (`c[k] += 1`) composes `[]` and `[]=`. Operator methods are
  instance-only: a top-level operator definition is a compile error.
