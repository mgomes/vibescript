- **Fixed docs: a quoted hash key is a string, not a symbol.** The language
  reference stated that `"foo-bar":` was the same symbol as `:"foo-bar"`, so an
  author following it read the value back with `h[:"foo-bar"]` and silently got
  nil. The reference now states the rule alongside the hash-literal grammar: a
  bare label makes a symbol key, a quoted label makes a string key, and the
  quoted form is the only literal syntax for the string-keyed hashes that
  `JSON.parse` returns.
