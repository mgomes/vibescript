- **Added: uppercase `%W` and `%I` percent arrays.** These are the
  interpolating companions to `%w` and `%i`: each entry is processed with
  double-quoted string semantics, so `#{...}` is expanded and the usual escape
  sequences (`\t`, `\n`, and so on) apply, while entries still split on
  whitespace that is neither escaped nor inside an interpolation. `%W` builds an
  array of strings and `%I` builds an array of symbols, matching Ruby. The
  lowercase `%w`/`%i` forms keep their literal behavior unchanged.
