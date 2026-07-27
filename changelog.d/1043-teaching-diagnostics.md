- **Improved: diagnostics for deliberate Ruby divergences now name the
  alternative.** `attr_accessor` points at `property`/`getter`/`setter` and notes
  the name is bare rather than a symbol; `property :x` shows the bare form;
  `class B < A` says inheritance is unsupported and names `include`; and an
  undefined variable that *is* bound at the top level explains that functions do
  not capture top-level bindings. An agent reads the error far more often than
  it reads the docs, so the error is where the idiom has to be taught.
