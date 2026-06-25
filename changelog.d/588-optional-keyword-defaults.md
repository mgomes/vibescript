- **Added: Ruby-style optional keyword-only parameters.** `def f(a: 0)` now
  declares an optional keyword-only parameter whose default applies when the
  label is omitted, distinct from the required keyword form `a:` and the typed
  positional form `a: int`. A later default may reference an earlier parameter
  (`def g(a:, b: a + 1)`), keyword-only parameters still reject positional
  arguments, and `a: nil` reads as the keyword default `nil`. Defaults evaluate
  under the sandbox step and memory quotas. The token after the colon
  disambiguates the forms: a bare type name (or `nil` used as a type) stays a
  typed positional parameter, so wrap a bare-identifier default in parentheses
  (`a: (other)`) to force the keyword form.
