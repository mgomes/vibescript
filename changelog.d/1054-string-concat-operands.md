- **Fixed: concatenating nil or a container into a string is now an error.**
  `"Hello, " + name` silently produced `"Hello, "` when `name` was nil, so a
  missing value disappeared into the message instead of being reported, and
  `[1] + "a"` produced `"[1]a"`. Concatenating scalars (`"total: " + count`)
  is unchanged.
