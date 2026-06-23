- **Performance: reduced large-document LSP diagnostics and symbol allocation
  churn.** `textDocument/documentSymbol` now builds the outline from typed
  structs instead of nested `map[string]any` values, and the script-local
  completion index is built lazily on the first completion request rather than
  on every diagnostics publish. Repeated edits in large files no longer pay to
  clone every compiled function or to allocate per-symbol range maps when no
  completion is requested. The wire output for diagnostics, document symbols,
  and completions is unchanged.
