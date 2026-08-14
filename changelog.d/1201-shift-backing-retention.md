- **Fixed: an array drained with `shift` no longer holds the slots it gave up.**
  Removing from the front narrows the receiver onto a window further into the
  same allocation, and Go keeps an allocation live as a whole for any pointer
  into it, so the vacated slots stayed held by an array that could no longer
  reach them. The memory estimator prices a header at its capacity plus its
  elements, both of which a forward reslice shrinks, so it stopped charging for
  them too: a 4096 element array drained to one element was charged 377 bytes
  while holding 131,072, and sixty-four rounds of building and draining one held
  7.98 MiB against a charge that rose by 10,563 bytes, without bound. A fully
  drained array now moves onto fresh empty storage, and a partially drained one
  is compacted once the prefix it has vacated grows larger than what it still
  shows. `pop` was unaffected in its charge, since it does not move the start of
  the header, but it held the same storage and now releases it too.
- **Changed: a `shift` that compacts allocates and can be refused.** The copy is
  charged and reserved before it is made, so a shift that cannot fit one is
  rejected and leaves the array holding every element it did. Draining costs
  9.00 steps per element where it cost 8.00, and stays linear: a copy of m
  elements happens only after more than m have been removed, so a drain of any
  size copies fewer elements than it removes.
