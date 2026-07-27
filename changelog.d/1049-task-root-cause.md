- **Fixed: a canceled sibling task reported `context canceled` instead of the
  failure that caused it.** When one task in `Tasks.run` fails its siblings are
  canceled, and reading a canceled handle surfaced the cancellation -- the
  mechanism rather than the reason -- so which error you saw depended only on the
  order the handles were read. It was also unrescuable, since a bare context
  error never becomes a script error. Both follow from surfacing the root cause.
