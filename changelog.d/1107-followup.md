- **Fixed: an absorbed `break` bypassed return-type validation and capability
  binding.** A break becoming a call's result skipped the function's declared
  return type -- so a string could leave an `-> int` function -- and skipped the
  post-call capability scan, leaving a builtin published during that call
  reachable without its contract. The break now continues down the normal
  return path.
