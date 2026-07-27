- **Fixed: an internal key leaked into `String#match` results.** The result
  carried a NUL-prefixed sentinel holding the positional values, visible in
  `keys`, `values`, `to_a`, `size`, `each`, and `inspect` -- a key the author
  never created and which read back as nil through its own name. The positional
  view is now rebuilt from the public entries, with the whole match under `to_s`
  as in Ruby, so no separate copy exists to leak.
