- **Added: Ruby-style hash value omission shorthand.** A label key followed
  immediately by `,`, `}`, or end-of-input now reads the local variable of the
  same name, so `{ name: }` is shorthand for `{ name: name }`, matching the
  call-site keyword shorthand (`greet name:`). Omission applies only to label
  keys; quoted keys such as `{ "name": }` are still rejected, and an undefined
  local reports the usual undefined-variable error.
