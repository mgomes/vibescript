- **Changed: Ruby-named collection mutators now mutate their receiver in
  place.** Arrays and hashes are mutable objects with Ruby's reference
  semantics: two variables bound to the same collection observe each other's
  mutations, `equal?` reports object identity (independently constructed empty
  arrays are now distinct objects), and `dup`/`clone` still detach a deep copy.
  Array `push`/`append`, `prepend`/`unshift`, `<<`, `insert`, `fill`, `clear`,
  and `delete_if`/`keep_if` mutate and return the receiver; `pop`, `shift`, and
  `delete` mutate and return the removed value(s) instead of the former
  `{ array:, popped: }`-style result hashes; `map!`, `sort!`, and `reverse!`
  transform in place and always return the receiver, while `select!`,
  `reject!`, `uniq!`, and `compact!` return `nil` when nothing changed. Hash
  `update`/`merge!` fold their arguments (and optional conflict block) into the
  receiver, `store` becomes true index assignment returning the stored value,
  `delete` returns the removed value, `clear` empties in place keeping the
  hash's default and identity, `delete_if`/`keep_if` prune in place, and
  `replace` adopts the argument's entries and default — all preserving
  Ruby-style insertion order. The non-mutating helpers (`map`, `select`,
  `merge`, `sort`, `+`, ...) are unchanged, string bang helpers keep their
  documented value-or-`nil` contract (strings remain immutable values), and
  host isolation is unchanged: arguments and globals are still deep-cloned per
  call, so in-script mutation never leaks into host originals. Iteration
  helpers walk the elements captured at entry, so structural mutation inside a
  block never extends or shortens the in-flight loop, and in-place growth is
  charged against the memory quota before it is allocated.
