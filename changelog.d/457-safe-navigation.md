- **Added: Ruby-style safe navigation operator (`receiver&.member`).** A safe
  navigation read or method call short-circuits to `nil` when the receiver is
  `nil`, and otherwise dispatches exactly like the corresponding `.` access. A
  short-circuited call evaluates neither its arguments nor its block, matching
  Ruby. The operator guards only its immediate access, so `user&.profile.name`
  still dispatches the trailing `.name` on whatever `user&.profile` returns. Safe
  navigation cannot appear anywhere in an assignment target, so `user&.name`,
  `user&.profile.name`, and `user&.items[0]` are all parse errors on the left of
  an assignment rather than silently assigning through `nil`.
