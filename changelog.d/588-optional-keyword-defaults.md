- **Added: Ruby-style optional keyword-only parameters.** `def f(a: 0)` now
  declares an optional keyword-only parameter whose default applies when the
  label is omitted, distinct from the required keyword form `a:` and the typed
  positional form `a: int`. A later default may reference an earlier parameter
  (`def g(a:, b: a + 1)`), keyword-only parameters still reject positional
  arguments, and `a: nil` reads as the keyword default `nil` while a nil-leading
  union annotation (`a: nil | int`) stays a typed positional parameter. Defaults evaluate
  under the sandbox step and memory quotas. The token after the colon
  disambiguates the forms: a bare type name stays a typed positional parameter,
  so wrap a bare-identifier default in parentheses (`a: (other)`) to force the
  keyword form. Expression defaults are supported, including a comparison against
  an earlier parameter (`def f(limit:, ok: limit < 10)`) and a hash literal
  (`def f(opts: { retry: 3 })`, `def f(opts: {})`, or one with `nil` values like
  `def f(opts: { previous: nil })`); the `name: { field: Type }` shape-type
  spelling stays a typed positional parameter, and a built-in generic container
  type (`def f(array, values: array<int>)`) is never shadowed by a value local
  of the same name. A brace group whose field values all parse as types but whose
  shape is structurally invalid, whether a duplicate field
  (`def run(payload: { id: string, id: int })`) or a missing field separator
  (`def run payload: { id: string name: int }`), surfaces its shape diagnostic
  instead of being silently reinterpreted as a hash default. A keyword default is
  never evaluated when the call shape can never
  bind: a missing required keyword or a leftover positional argument is reported
  before any default runs, so a default's side effects, errors, or quota cost
  cannot mask the real arity or keyword mismatch.
