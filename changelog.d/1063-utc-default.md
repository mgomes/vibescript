- **Changed: zoneless times default to UTC instead of the host zone.** `Time.now`
  and a `Time.parse` of a timestamp carrying no zone bound to the host process's
  `TZ`, so the same script and the same data produced a different calendar day
  depending on where it ran -- and `Time.now` disagreed with the `now` builtin,
  which is documented as UTC and always was. Both are UTC now. An explicit `in:`
  zone, or a `Z`/offset in the input, is honored as before.
