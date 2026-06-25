- **Added: Ruby-style `eql?` and `equal?` equality predicates.** Every value now
  answers `eql?` (hash-key equality, so `1.eql?(1.0)` is `false`) and `equal?`
  (object identity, so `1.equal?(1)` is `true` while two independently built
  arrays with equal contents are not `equal?`). The predicates report `false`
  rather than raising when the operands' kinds differ, and a class may override
  them with its own methods of the same name. Every empty hash and object now
  carries its own backing storage, so two independently built empties (including
  `{}` from `JSON.parse("{}")`) are distinct objects under `equal?`. Empty arrays
  are the one exception: any two empty arrays are `equal?` regardless of how they
  were produced, so `[1].select { |x| false }.equal?([])` is `true` even though
  `select` preallocates its result with spare capacity.
