- **Added: Ruby-style `Hash.new` defaults and `Hash#default` / `Hash#default_proc` readers.**
  `Hash.new(default)` builds a hash that returns `default` for a missing `[]`
  lookup without inserting it, and `Hash.new { |hash, key| ... }` installs a
  default proc invoked on a missing-key lookup (which inserts only if its body
  assigns one). The value and block forms are mutually exclusive, and bare
  `Hash.new` matches a `{}` literal with a `nil` default. `default` returns the
  configured default value (never running the proc, matching Ruby) and
  `default_proc` returns the configured proc. `[]` access, `dig`, and
  `values_at` all consult the default for a missing key (each is a `[]` lookup in
  Ruby), so `Hash.new(0).dig(:missing)` is `0` and a default proc fires per miss;
  `fetch` keeps ignoring the default, matching Ruby. The default travels with
  the hash through index assignment and is copied onto the result of `merge`
  (and its `update` / `merge!` aliases); every other transform returns a plain
  hash with no default. A default proc that escapes one `Script.Call` and is
  passed back into another (as an argument, global, or task-inherited hash) is
  re-rooted onto the current call, so a missing-key lookup resolves globals,
  capabilities, and functions against the current invocation rather than the
  stale environment it was created in.
