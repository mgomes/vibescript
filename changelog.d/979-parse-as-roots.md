- **Added: `JSON.parse_as` validates non-object JSON roots.** Array,
  primitive, nullable, and union contracts now work at the root —
  `JSON.parse_as(raw, array<int>)`, `JSON.parse_as(raw, int?)`,
  `JSON.parse_as(raw, int | string)` — using the same normalization and
  diagnostics as shape roots, with the declared contract carried as the
  checker's result fact. The type spelling is recognized in parenthesized
  call arguments; spellings that also read as values (a local named `int`)
  keep their value reading under the shape-literal shadowing rules.
