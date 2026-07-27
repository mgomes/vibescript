- **Fixed: `<=>` did not order symbols or nil though `sort` did.** `:a <=> :b`
  answered nil while `[:c, :a].sort` worked, so the operator misreported what the
  language can do. Coverage now follows Ruby per kind: symbols order under `<=>`
  and the relational operators (Symbol includes Comparable), nil orders under
  `<=>` alone, and booleans order under neither.
