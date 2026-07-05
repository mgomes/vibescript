- **Improved: LSP large-document response cost.** Diagnostics publishes now
  skip the recompile when the buffer text is byte-identical to the last
  compile, diagnostics payloads use typed structs instead of nested maps, and
  document-symbol outlines are cached per document and invalidated on every
  edit and fresh compile, keeping large buffers responsive on each keystroke.
- **Improved: watch-mode overhead on large module roots.** Full rescans reuse
  the previous snapshot's map storage and walk directory entries without
  per-file `FileInfo` allocations, and the polling loop's per-tick known-file
  check stats through a platform shim that avoids `os.Stat` allocations on
  macOS and Linux; symlink handling, added/deleted `.vibe` detection, and
  periodic full scans are unchanged.
