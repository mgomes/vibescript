- **Fixed: enum conversions rendered before checking the quota.** `to_s`,
  `string`, and `inspect` on an enum value allocated the whole `Enum::Member`
  text before the memory guard ran, which matters when an identifier is larger
  than the quota. The length is now projected from the two identifiers first.
  A typo near `string` also suggests it.
