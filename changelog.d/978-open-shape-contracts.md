- **Added: open shape contracts.** A trailing `...` marks a shape open:
  `{ name: string, ... }` requires and validates the declared fields (optional
  fields compose) while letting undeclared extra fields pass unchecked, and
  `{ ... }` alone accepts any hash. Shapes stay exact by default, open and
  exact shapes nest freely, `JSON.parse_as` carries extras through untouched,
  and the checker treats reads of undeclared fields on an open shape as
  unknown instead of `nil`.
