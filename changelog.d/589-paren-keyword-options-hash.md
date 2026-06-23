- **Fixed: parenthesized function calls bind keyword labels to a positional
  options hash like the parenless form.** When a plain function has no matching
  keyword parameter and exposes a positional options parameter,
  `configure(retries: 3)` now collapses its keyword labels into the options hash
  just as `configure retries: 3` already did, and a typed options parameter is
  validated against the synthesized hash so `configure(retries: "slow")` is
  rejected with the shape mismatch instead of `missing argument`. The same
  binding now applies when invoking a function value through its `call` alias, so
  `configure.call(retries: 3)` matches the direct `configure(retries: 3)` form,
  and when calling a function value held in a member such as a module function,
  so `helpers.configure(retries: 3)` matches `configure(retries: 3)` too.
  Constructor and method calls keep strict parenthesized keyword binding,
  including an instance method named `call`. A positional argument that follows a
  keyword label inside parentheses, such as `collect(first: 1, "tail")`, is now
  rejected with a parse error matching Ruby (which treats it as a syntax error)
  and the parenless form, rather than silently appending the synthesized options
  hash after the trailing positional.
