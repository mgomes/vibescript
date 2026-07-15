- **Removed: hash rocket (`=>`) syntax, again.** The Ruby stdlib compatibility
  batch (#867) unintentionally reinstated the hash-rocket literal syntax that
  #762 had removed, and #599 extended rockets into shape type annotations.
  Rockets in hash literals (`{ expr => value }`) and in shape annotations
  (`def f(p: { "user-id" => string })`) are parse errors again, reported as a
  single targeted hash-pair diagnostic. The supported spellings are unchanged:
  colon keys (`name:`, `"name":`) in hash literals, index assignment
  (`h[key] = value`) for runtime-computed keys, and colon shape-field
  separators, including string, symbol, and quoted-symbol field names. The
  arbitrary-key runtime behavior from #867 (integer, array, range, and other
  hashable keys with Ruby-style string/symbol identity) is retained; only the
  literal syntax is gone. Rescue bindings (`rescue RuntimeError => err`) are
  unaffected.
