- **Fixed: a source of malformed percent-array candidates no longer makes
  parsing superlinear.** Deciding whether a `%` the lexer read as modulo really
  opens a `%w`/`%i`/`%W`/`%I` literal means re-scanning from that point, and the
  scan can only report failure by reaching the end of the input, after which the
  caller advances a single byte to the next candidate. A source that just
  repeats ` %w[` therefore paid for a near-full re-scan per byte, and because
  each of those scans re-entered the interpolation finder for every `#{` it
  crossed, a source of many small interpolations was cubic rather than quadratic.
  Only the source-size limit stood in the way, so a script at the default 1 MiB
  cap held the parser for over eight minutes before any runtime quota could
  apply; it now finishes in 149ms. The fruitless scans are charged against one
  parse-wide allowance of four times the source length, shared by every lexer
  and sub-parser involved, while a scan that does find its literal stays free --
  so percent literals of any length and number keep parsing as before.
