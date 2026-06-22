- **Added: Ruby-style `Time#to_a` tuple conversion.** `Time#to_a` returns the
  positional field tuple `[sec, min, hour, mday, month, year, wday, yday, isdst,
  zone]`, matching Ruby for compatibility with positional field processing. Field
  values reuse the existing `Time` accessors, so UTC, local, and offset receivers
  stay consistent across both forms.
