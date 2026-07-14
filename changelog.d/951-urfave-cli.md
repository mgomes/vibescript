- **Changed: the `vibes` CLI now uses urfave/cli v3.** Every command has
  generated `--help` output; successful help now exits zero on stdout. Flag
  parsing still stops at the first positional argument so script arguments
  keep their existing behavior. Unknown commands now name the rejected token,
  and `analyze`, `lsp`, `repl`, and `help` reject undocumented extra positional
  arguments instead of ignoring them.
