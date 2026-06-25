- **Added: Ruby-style `inspect` debug representations.** Every core value kind
  (`nil`, booleans, integers, floats, strings, symbols, arrays, and hashes) now
  responds to `inspect`, returning a parseable debug string. Unlike the
  interpolation/`to_s` rendering, `inspect` keeps quotes and escaping for strings
  (`"a\nb".inspect` is `"\"a\\nb\""`), renders symbols with their leading colon
  (`:ok.inspect` is `":ok"`), and recurses into arrays and hashes
  (`[1, "x", nil].inspect` is `"[1, \"x\", nil]"`). Hashes render in Vibescript's
  colon-label key form rather than Ruby's unsupported hash-rocket syntax, so the
  output round-trips as a Vibescript literal. `inspect` takes no arguments, and
  the rendered size is charged against the memory quota before the string is
  built so inspecting a huge composite fails with a quota error instead of
  allocating an oversized result.
