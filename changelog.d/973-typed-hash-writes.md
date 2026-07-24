- **Improved: the checker reports incompatible writes to typed hashes and
  shapes.** A local known to be `hash<K, V>` now checks `h[k] = v`, `store`,
  and the in-place `merge!`/`update` against the key and value bounds, and a
  local with a declared shape type checks field writes against their declared
  types — including a statically known extra field on an exact shape. Hash
  and shape literals stay freely writable (their known fields update in
  place), and unknown keys, values, and receivers stay gradual.
