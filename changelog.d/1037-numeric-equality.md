- **Fixed: `==` compares an int and a float numerically.** `1 == 1.0` was false,
  so an average computed as `sum / 3.0` never matched the integer it printed as,
  with no diagnostic. `eql?` keeps its kind gate — that is the distinction the
  documentation draws — so hash keys still treat `1` and `1.0` as different.
