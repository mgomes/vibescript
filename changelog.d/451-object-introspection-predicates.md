- **Added: Ruby-style object introspection predicates.** Every value now
  responds to `respond_to?`, `is_a?`, `kind_of?`, and `instance_of?`.
  `respond_to?(name)` reports whether the receiver has a callable member of that
  name (a symbol or string), excluding data such as hash keys, namespace
  constants, and instance variables, and honoring privacy (with the optional
  `include_all` second argument). `is_a?`/`kind_of?`/`instance_of?` test whether
  the receiver is an instance of a given script class; without inheritance they
  test direct class identity. A script class may override any of these with its
  own method definition.
