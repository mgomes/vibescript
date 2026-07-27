- **Added: `vibes check` reports an unknown method on a statically-known
  receiver.** `s.uppercase` on a `s: string` parameter reported "No issues
  found" and failed only at runtime. Choosing a wrong method name is the most
  frequent authoring mistake there is, and it was previously unassisted at check
  time. Receivers that could dispatch elsewhere -- dynamic, named types, and the
  kinds whose member list is hand-maintained -- stay silent.
