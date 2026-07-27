- **Fixed: a keyword default referencing an earlier parameter reported parameter
  ordering.** `def f(a:, b: a)` -- the simplest spelling of the documented "a
  later default may reference an earlier parameter" feature -- parses the bare
  identifier as a type annotation, which makes the parameter ordinary and trips
  the ordering rule. The error now explains the type-annotation reading and shows
  the parenthesized form (`b: (a)`) that expresses the default.
