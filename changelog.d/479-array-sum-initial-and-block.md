- **Changed: `Array#sum` now honors a Ruby-style initial value and block.**
  `sum(initial)` seeds the accumulator instead of starting from `0`
  (`[1, 2, 3].sum(10)` is `16`, `["a", "b"].sum("")` is `"ab"`), and a block
  transforms each element before it is added (`[1, 2, 3].sum { |n| n * 2 }` is
  `12`, with `sum(initial) { ... }` combining both). Previously the argument and
  block were silently ignored. Each addition must operate on compatible operands,
  mirroring Ruby's `+`, so summing a string with a non-string (such as the
  default `0` accumulator against string elements) raises instead of silently
  coercing the operands.
