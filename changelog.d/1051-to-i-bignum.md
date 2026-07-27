- **Fixed: `String#to_i` rejected integers beyond int64.** A value the language
  can represent, print, and compute with could not be parsed back from its own
  string form, so `(2 ** 100).to_s.to_i` failed. Integers are arbitrary
  precision, and the surfaces documented as staying within 64 bits are indexes,
  counts, sizes, precisions, and the temporal types -- a value conversion is none
  of those. `to_int` had the same gap on strings while already promoting floats,
  and is fixed with it. Malformed strings are still rejected; only the range
  limit is lifted.
