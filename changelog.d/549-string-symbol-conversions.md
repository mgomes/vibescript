- **Added: Ruby-style string/symbol conversion helpers.** `String#to_sym` and
  its alias `String#intern` return the symbol named by the receiver, accepting
  any contents verbatim (including whitespace, punctuation, and the empty
  string). `Symbol#id2name` and `Symbol#to_s` return the symbol's name as a
  string, and `Symbol#to_sym` returns the receiver. The pair round-trips between
  the two representations, and symbol/string equality stays kind-sensitive so
  `:name == "name"` is `false`.
