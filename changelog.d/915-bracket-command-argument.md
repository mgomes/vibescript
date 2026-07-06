- **Added: array literals as parenless command arguments.** A bracket
  detached from a non-local callee now opens an array-literal argument,
  matching Ruby's spacing rule: `puts [3, 1, 2].sort` is
  `puts([3, 1, 2].sort)`, and further arguments may follow
  (`concat [1], [2]`). A flush bracket keeps the indexing reading —
  `puts[1]` still tries to index the callee — and a known local indexes in
  every spacing, so `a [0]` and `a [0] = 1` are unchanged when `a` is a
  local.
  Breaking edge: bare accessor names in method bodies are non-local callees,
  so `items [0]` with `getter items` now passes an array argument (an arity
  error at runtime, exactly as in Ruby) instead of indexing the accessor
  value — index through a receiver (`self.items[0]`) or a flush bracket.
  `self [0]` is pinned to keep indexing: `self` is never a command callee.
