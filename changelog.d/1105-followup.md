- **Fixed: `vibes check` rejected a hash's stored entries.** `({answer: 42}).answer`
  returns 42 at runtime but was reported as an unknown member: a hash serves
  stored entries for any name its member table does not own, so that table is
  not the authoritative set the check assumed. Regex literals are now detected
  as receivers, which the check claimed to cover but could not reach.
