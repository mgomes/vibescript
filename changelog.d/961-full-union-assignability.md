- **Improved: known unions must fully satisfy typed boundaries.** Calls,
  defaults, and returns now reject finite inferred unions when any arm can
  violate the required type, including nested nullable, array, hash, and shape
  facts. `any`, unknown JSON and host values, and dynamic dispatch continue to
  defer to runtime validation.
