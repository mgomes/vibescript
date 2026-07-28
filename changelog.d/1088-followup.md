- **Hardened: `flat_map` charges the block's returned array while copying it.**
  A block that returns a fresh array keeps it live while its elements are copied
  into the result, and the build accumulator's baseline covers the receiver,
  arguments, and block but not a block's return value. The widest such result is
  now charged, closing the gap without accumulating a charge per iteration.
