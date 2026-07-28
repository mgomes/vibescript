- **Fixed: `retry` escaped an implicit `to_s`.** A class's `to_s` invoked by
  interpolation inside a rescue handler could run `retry` and restart the
  caller's rescue, while an explicit `obj.to_s` in the same position reported
  `retry cannot cross call boundary`. The implicit call is a call boundary too
  and now behaves like one.
