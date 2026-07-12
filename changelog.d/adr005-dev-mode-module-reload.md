- **Added: `Config.DevMode` for development-time module reloading (ADR-005).**
  When enabled, every `require` revalidates its cached module against the
  source file's mtime+size and recompiles it on change, and require misses are
  re-resolved from disk so newly created modules load without a restart. The
  zero value keeps production behavior: modules compile once and are served
  from cache until `ClearModuleCache`.
