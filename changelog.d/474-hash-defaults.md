- **Added: Ruby-style `Hash.new` defaults and `Hash#default` / `Hash#default_proc` readers.**
  `Hash.new(default)` builds a hash that returns `default` for a missing `[]`
  lookup without inserting it, and `Hash.new { |hash, key| ... }` installs a
  default proc invoked on a missing-key lookup (which inserts only if its body
  assigns one). The value and block forms are mutually exclusive, and bare
  `Hash.new` matches a `{}` literal with a `nil` default. `default` returns the
  configured default value (never running the proc, matching Ruby) and
  `default_proc` returns the configured proc. Only `[]` access consults the
  default; `fetch`, `dig`, and `values_at` ignore it. The default travels with
  the hash through index assignment and is copied onto the result of `merge`
  (and its `update` / `merge!` aliases); every other transform returns a plain
  hash with no default.
