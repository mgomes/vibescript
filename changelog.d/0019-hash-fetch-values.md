- **Added: Ruby-style `Hash#fetch_values`.** `Hash#fetch_values(*keys)` returns
  the values for several keys at once, in the requested order. Unlike
  `values_at`, it raises a `key not found` error for any missing key; pass a
  block to compute a replacement value for each missing key instead of raising.
