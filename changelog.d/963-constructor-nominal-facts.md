- **Improved: constructor results carry nominal class facts.** A statically
  resolved `User.new` now infers as a `User` instance, so the fact flows
  through locals, branches, arguments, and returns, instance methods resolve
  their shapes and result types from the fact, and shadowed class names or
  dynamic constructor dispatch stay unknown.
