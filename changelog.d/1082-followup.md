- **Fixed: a duplicate named capture reported nil.** When a pattern reuses a
  group name, only one of those groups participates in a given match, but a
  later non-participating duplicate overwrote an earlier match -- so
  `/(?<x>a)|(?<x>b)/` against `"ab"` reported nil for `x` rather than `"a"`. The
  last participating group is kept, as in Ruby.
