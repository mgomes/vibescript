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
- **Fixed: an array's wrapper is charged the memory it actually occupies.** The
  struct the runtime boxes every array's elements in was exactly a slice header,
  so the estimator's slice-base charge priced it by accident. The shrink above
  adds a field to it, which would have left 8 bytes per array unmetered -- an
  under-count introduced by a fix for an under-count -- so the charge is now
  derived from the struct and the next field is priced by the commit that adds
  it. Every projection that reserves a new array was moved with it, so a build
  and the walk that supersedes it still agree to the byte -- including the
  destructured-rest and range/string materialization preflights, which promise
  the allocation will fit before making it and could otherwise allocate an array
  wrapper with 8 bytes fewer than it needs. Adding the two constants together to
  price an array is now a build failure rather than a convention, since the
  helper alone does not stop the next projection from restating the old formula.
- **Fixed: the estimated slice header size is derived rather than stated, so
  memory accounting is correct on 32-bit targets.** It was hard-coded to the
  64-bit value of 24 bytes, which over-charged every slice, string table and
  array by 12 bytes on the 386 builds in the release matrix. Deriving the array
  wrapper's charge from its struct is what exposed it: the two are subtracted
  from each other, and on 386 the stated 24 exceeded the whole 16-byte wrapper.
  Nothing changes on 64-bit, where the derived value is the same 24.
- **Fixed: builtins that publish inner arrays reserve the wrapper each one
  allocates.** `zip`, `product`, `combination` and `permutation` priced every row
  as a bare slice, so a wide call allocated one array wrapper per row beyond what
  the quota had admitted; `group_by` did the same once per group, and
  `String#scan` once per match with captures. `partition` missed the two its
  result holds. The projection helpers now name which of the three referents they
  price -- an array that owns its Value, an inner array whose Value a surrounding
  backing already counts, or a slice nothing ever boxes -- since the arithmetic
  is identical and only the referent differs.
