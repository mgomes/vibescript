- **Fixed: `Array#join` joins nested arrays recursively like Ruby.** `join` now
  flattens nested arrays into the output using the active separator instead of
  rendering their inspect form, so `[1, [2, 3], 4].join("-")` is `"1-2-3-4"` and
  `[1, [2, [3, 4]], 5].join("-")` is `"1-2-3-4-5"`. Scalar elements are unchanged:
  `nil` still contributes an empty segment (`[1, nil, "x"].join(",")` is `"1,,x"`)
  and an empty array still joins to `""`.
