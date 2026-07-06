- **Fixed: `vibes lsp` releases per-document state on `textDocument/didClose`.**
  Closing a file now evicts its document text, compiled-script, navigation,
  completion, diagnostics, and outline caches and publishes an empty
  diagnostics set so editors clear stale squiggles. Previously the server kept
  every document's state for the life of the process, so long editor sessions
  touching many files grew memory unboundedly.
