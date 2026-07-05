- **Added: Ruby syntax follow-up batch.** Comma-separated `return` values,
  double-quoted hex/unicode escapes, typed `raise Type, "message"`, multiline
  bounded range endpoints, order-independent enum union normalization, scoped
  class constants (`Config::LIMIT`), `alias`/`alias_method`, qualified module
  enum type annotations (`mod.Status`), and parenless operand/block forms.
- **Changed: a same-line `do` block after a parenless call argument now binds
  to the outer call, matching Ruby.** `puts arr.map do |x| ... end` passes the
  block to `puts` (which ignores it), so `map` raises "requires a block";
  parenthesize the receiver call (`puts(arr.map do |x| ... end)`) or use a
  brace block, which keeps binding to the nearest call.
