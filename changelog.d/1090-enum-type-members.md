- **Added: `name`, `to_s`, and `inspect` on an enum type.** The type object
  supported no member access at all -- it interpolated as `<Enum Status>` but
  `Status.to_s` reported "unsupported member access on enum" -- leaving it the
  one value kind you could not ask anything about.
