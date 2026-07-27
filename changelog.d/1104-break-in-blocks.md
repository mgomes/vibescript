- **Added: `break` works inside blocks.** The most common early-exit idiom in a
  Ruby-flavored language was rejected, and the restriction was asymmetric --
  `next` and `return` both crossed a block boundary and `break` did not. As in
  Ruby, `break` now terminates the call the block was passed to and that call
  evaluates to the break value: `[1, 2, 3, 4].each { |n| break n if n > 3 }` is
  `4`. A break crossing a call that received no block still reports.
