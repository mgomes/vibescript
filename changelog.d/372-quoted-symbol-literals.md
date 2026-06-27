- **Added: quoted symbol literals.** Symbols can now be written with double or
  single quotes (`:"foo-bar"`, `:'foo bar'`, `:""`) so they can hold
  punctuation, spaces, or be empty, matching Ruby. Quoted symbols use the same
  escapes as the matching string quote and are accepted anywhere a symbol
  literal is, including type-shape field names. A quoted-string hash key such as
  `"foo-bar":` is the same symbol as `:"foo-bar"`. Interpolation inside symbol
  literals is not supported, so `:"a#{b}"` is a parse error.
