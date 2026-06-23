- **Fixed: `String#chr` returns an empty string for an empty receiver like Ruby.**
  `"".chr` now returns `""` instead of `nil`, so `String#chr` always returns a
  string. Non-empty receivers are unchanged, so `"abc".chr` still returns `"a"`.
