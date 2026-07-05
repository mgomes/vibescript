- **Fixed: AST cloner dropped mixin and visibility class members.** The
  definition cloner in `internal/ast` silently omitted `include`/`extend`
  and visibility declarations from cloned class member lists and shared
  nested module declarations with the original. The parser fuzz target's
  completeness invariant also lagged the last several syntax additions,
  rejecting valid parses of endless/beginless ranges, splat and keyword-splat
  arguments, block-pass arguments, mixin and visibility members, empty quoted
  symbols, and bare rest-discard assignments; each form is now accepted,
  seeded into the fuzz corpus, and pinned by a coverage test that fails
  deterministically when a new AST shape is missed. (#902)
