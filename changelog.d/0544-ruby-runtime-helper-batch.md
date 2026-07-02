- **Added: Ruby-style dynamic dispatch, comparable, collection, and string
  helper coverage.** Values now answer `send` and `public_send`, with
  `public_send` preserving public visibility while `send` can reach private
  methods; instance `initialize` methods are private constructor hooks by
  default. Comparable scalar families now support `between?`. Arrays and hashes
  gain Ruby-shaped filtering, clearing, bang-transform, sampling, shuffling,
  rotation, product, combination, permutation, and adjacent chunking helpers,
  with method-based collection helpers keeping Vibescript's immutable return
  model. Strings now support Ruby character-set helpers `count`, `delete`,
  `tr`, and `squeeze` including ranges and leading-complement sets.
