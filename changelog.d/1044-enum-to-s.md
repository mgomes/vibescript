- **Added: `to_s` and `inspect` on enum values.** Interpolation rendered an enum
  value correctly but the explicit conversion did not exist, so `.to_s` reported
  an unknown member -- with no suggestion, since neither `name` nor `symbol` is
  close enough for the did-you-mean to fire. Both return exactly what
  interpolation produces.
