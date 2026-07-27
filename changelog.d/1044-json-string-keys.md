- **Documented: `JSON.parse` produces string keys.** A hash literal writes symbol
  keys, so a parsed object never equals the literal it came from and a symbol
  lookup reads nil rather than reporting anything -- the first symptom was
  usually a downstream nil error far from the parse. `docs/builtins.md` now
  states this next to `JSON.parse` and shows the `transform_keys` conversion.
