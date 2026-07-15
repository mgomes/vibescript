- **Added: LSP hover and completion documentation.** Hovering a builtin, a
  namespace member (`JSON.parse_as`, `Math.sqrt`), or a keyword now shows its
  documentation from `docs/builtins.md`, and completion items carry the same
  docs plus signature details. The reference gained entries for the output,
  proc/lambda, `Regexp`, `Duration`, `Time`, and `Tasks` builtins, with drift
  gates so new builtins cannot ship undocumented.
- **Added: LSP hover for value member methods.** Hovering a method after a
  `.` receiver (`items.map`, `name.upcase`, `h.fetch`) now shows its entry
  from the stdlib reference and the per-type guides; names shared by several
  types (`size`) render one section per receiver type. Unambiguous members
  carry the same docs in after-dot completion, and a drift gate keeps the
  parsed table honest against the runtime's member dispatch.
- **Added: LSP hover for user-defined symbols.** Hovering a function, class,
  module, enum, or enum member declared in the current document shows its
  reconstructed signature (parameter types, defaults, return type) plus the
  `#` comment block above the declaration.
