- **Added: LSP hover and completion documentation.** Hovering a builtin, a
  namespace member (`JSON.parse_as`, `Math.sqrt`), or a keyword now shows its
  documentation from `docs/builtins.md`, and completion items carry the same
  docs plus signature details. The reference gained entries for the output,
  proc/lambda, `Regexp`, `Duration`, `Time`, and `Tasks` builtins, with drift
  gates so new builtins cannot ship undocumented.
