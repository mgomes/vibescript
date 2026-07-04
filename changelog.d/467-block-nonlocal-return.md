- **Changed: `return` inside a block returns from the enclosing method.**
  Matching Ruby's non-local return, an explicit `return` in a normal block now
  returns from the method whose body created the block — ending iteration
  immediately — instead of acting as a block-local return that let the method
  continue. The unwind runs `ensure` blocks, cannot be intercepted by `rescue`,
  and validates typed returns as usual; a block invoked after its method has
  already returned raises `LocalJumpError`. Blocks that relied on `return` for
  an early block-local value should make the value the block's last expression.
