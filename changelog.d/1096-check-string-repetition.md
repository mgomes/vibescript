- **Fixed: `vibes check` rejected `"-" * 12`.** `String#*` works at runtime, but
  the checker's operator matrix had no string case, so it reported unsupported
  multiplication operands on valid programs.
