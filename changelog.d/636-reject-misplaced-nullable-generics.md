- **Fixed: reject misplaced nullable generic type syntax.** The parser no longer
  accepts a `?` on a generic container name before its type arguments, so
  `array?<int>` and `hash?<string, int>` now raise a parse error pointing to the
  documented spelling (`array<int> | nil`) instead of silently parsing as a
  nullable `array<int>` / `hash<string, int>`. Untyped nullable containers such
  as `array?` and `hash?` (without type arguments) are still accepted.
