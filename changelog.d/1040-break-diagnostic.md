- **Fixed: a `break` inside a block reported "break used outside of loop".** The
  message described a situation the author could see was untrue -- the `break`
  was plainly inside an `each` -- so it sent them looking for a missing `end`.
  It now names the real restriction and points at the alternatives. `break` still
  cannot cross a block boundary; only the diagnostic changed.
