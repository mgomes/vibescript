- **Added: Ruby-style `Time#strftime` formatting.** `Time#strftime` accepts a
  Ruby percent format string so Ruby date-formatting code runs unchanged, sitting
  alongside the existing Go-layout `Time#format`. The supported directive subset
  covers year/month/day, 12- and 24-hour time, minute/second, sub-second
  (`%L`, `%N`, and widths like `%6N`), weekday and month names, weekday numbers
  (`%w`/`%u`), epoch seconds (`%s`), UTC offset (`%z`, `%:z`, `%::z`), zone name
  (`%Z`), the `%n`/`%t`/`%%` escapes, and the compound shortcuts
  `%F`/`%T`/`%X`/`%R`/`%D`/`%x`/`%r`/`%c`. Directives honor Ruby's flags and
  width between the `%` and the letter: `-` (no padding), `_` (space padding),
  `0` (zero padding), `^` (uppercase), and `#` (toggle case), with an optional
  width applied to every numeric and name directive (so `%-d` is `2`, `%6Y` is
  `002024`, and `%^B` is `JANUARY`). Unknown directives pass through verbatim
  like Ruby (`%Q` stays `%Q`), `%Z` mirrors `Time#zone` (so fixed-offset
  receivers render their offset name rather than Ruby's empty string), and a
  trailing `%` (or a modifier with no directive) raises a clear runtime error.
