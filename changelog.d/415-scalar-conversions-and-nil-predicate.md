- **Added: Ruby-style scalar conversion methods and the `nil?` predicate on
  core values.** Every value now answers `nil?` (true only for `nil`),
  including script class instances, classes, function values, and enum values;
  it resolves through the universal `Object#nil?` fallback, so a user-defined
  `nil?` keeps precedence. The scalar kinds whose display form is
  bounded by their own footprint (`nil`, booleans, integers, floats, strings,
  symbols, money, durations, and times) also answer `to_s` and the documented
  `.string` conversion idiom from `docs/typing.md`. Arrays, hashes, and ranges
  deliberately do not gain `to_s`/`string` because their rendering can be
  arbitrarily large; they continue to expose `inspect`, which projects the
  rendered length against the memory quota before allocating. Integers and
  floats convert between numeric kinds with `to_i`/`to_f` (`Float#to_i`
  truncates toward zero like Ruby and raises on a non-finite or out-of-range
  value). Strings parse numeric text with `to_i`/`to_f`; unlike Ruby's lenient
  `String#to_i`/`String#to_f`, these are strict like the global
  `to_int`/`to_float` and raise on an empty, non-numeric, or non-finite string
  so a malformed value never silently becomes zero at a typed boundary.
