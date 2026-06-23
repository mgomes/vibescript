- **Fixed: parenless `Hash#merge` returns a copy of the receiver like Ruby.**
  A bare `hash.merge` (and its aliases `hash.update` and `hash.merge!`) now reads
  as a zero-argument call that returns a copy of the receiver instead of leaking
  the unbound method value. This matches Ruby, where the call has no parentheses
  distinction, so `{ a: 1 }.merge` and `{ a: 1 }.merge()` both return `{ a: 1 }`.
