- **Fixed: typed hash boundaries no longer copy conforming payloads.** Passing
  a hash through a `hash<K, V>` parameter or return annotation used to build a
  full output copy before discovering that no value needed coercion, roughly
  doubling the allocation cost of a no-op boundary crossing (a conforming
  10,000-entry `hash<string, int>` argument paid for two hashes instead of
  one). Normalization now validates in place and returns the original
  container when nothing changes; small symbol-keyed hashes also skip the
  temporary entry materialization during validation. When a value does need
  coercion (for example a symbol against an enum-valued hash), the copy still
  carries every already-validated entry in insertion order, normalizes the
  Ruby-style default value, and preserves the hash-versus-object receiver
  kind, exactly as before.
