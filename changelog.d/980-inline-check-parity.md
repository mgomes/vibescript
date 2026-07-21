- **Fixed: inline snippets check like files.** `vibes run -check -e` now runs
  the same whole-script pass as `vibes check`, so a typed contradiction inside
  a function or method the snippet never calls is reported identically for
  equivalent inline and file sources. Top-level execution order and require
  semantics are unchanged.
