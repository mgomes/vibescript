- **Changed: `String#sub`/`gsub` regex replacements use Ruby backreferences.**
  With `regex: true`, `sub`, `sub!`, `gsub`, and `gsub!` now expand
  replacement strings using Ruby's substitution syntax instead of Go's. `\1`–`\9`
  insert capture groups, `\&` (or `\0`) the whole match, `` \` `` and `\'` the
  pre/post-match, `\+` the last participating group, `\k<name>` a named group,
  and `\\` a literal backslash; `$1` and `$&` are now literal text. This makes
  Ruby replacement strings copied into Vibescript produce the same output, so
  `"abc123".sub("([a-z]+)([0-9]+)", "\\2-\\1", regex: true)` yields `"123-abc"`.
  As in Ruby, once a pattern defines any named capture group the numbered refs
  `\1`–`\9` expand to the empty string (use `\k<name>` instead), so
  `"John Smith".sub("(?<first>\\w+) (?<last>\\w+)", "\\2, \\1", regex: true)`
  yields `", "`; the whole-match, pre/post-match, and `\k<name>` refs keep
  working in that mode. An unterminated `\k<name` or a `\k<name>` that names an
  undefined group raises an error, matching Ruby. (`Regex.replace`/
  `Regex.replace_all` keep their existing `$1` syntax.)
