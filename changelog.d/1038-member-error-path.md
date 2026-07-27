- **Fixed: an unknown method now reports the offending line and can be rescued.**
  Calling a method that does not exist on a string, array, hash, money, or
  temporal value reported the start of the script instead of the call, and could
  not be caught by `rescue` at all. Sandbox limits stay deliberately outside
  `rescue`, as before.
