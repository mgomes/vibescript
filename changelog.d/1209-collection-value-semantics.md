- **Changed: arrays and hashes are values.** Binding, passing, or returning a
  collection produces another logical value, so updating one binding or path can
  no longer be seen through a sibling ([ADR-006](docs/adr/006-slim-language-for-predictable-sandboxing.md)
  item 2). A mutating operation updates the local, instance variable, or nested
  path its receiver names; a receiver naming no such path is a temporary whose
  update is returned and reaches nothing else. `==` remains content equality and
  `equal?` now answers the same question, since collections carry no identity.
  The runtime uses copy-on-write internally: binding stays free and a copy is
  paid only where a write meets a value something else can still see.
- **Removed: the collection bang variants that duplicated a non-bang form.**
  `map!`, `sort!`, `reverse!`, `uniq!`, `compact!`, `select!`, and `reject!` on
  arrays, and `merge!` with its alias `update` on hashes. Reassign the non-bang
  result (`a = a.sort`, `h = h.merge(other)`), or use `keep_if` / `delete_if`
  where `select!` / `reject!` updated in place.
