- **Fixed: concatenating a non-renderable value into a string is now an error.**
  `"Hello, " + name` silently produced `"Hello, "` when `name` was nil, so a
  missing value disappeared into the message instead of being reported, and
  containers, host objects, class instances, and callables rendered as
  placeholders such as `"[1]a"` or `"user: <User instance>"`. Concatenating
  scalars (`"total: " + count`) is unchanged, and the checker now reports the
  same mismatch statically when both operand types are known.
