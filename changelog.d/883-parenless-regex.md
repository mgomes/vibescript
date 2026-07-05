- **Added: parenless command-argument regex literals.** A regex literal can
  now be a parenless call argument, matching Ruby: `match /ID-[0-9]+/` parses
  as `match(/ID-[0-9]+/)`, with flags, escapes, and further arguments working
  exactly like the parenthesized form (`scan /a+/, text`). The parser feeds
  its local-variable table into the decision, so a slash after a known local
  keeps dividing in every spacing — `total /2`, `total /(n + 1)`, and
  `total /-n` stay arithmetic.
- **Changed: `f /2` where `f` is a zero-arg function or member call now opens
  a regex literal.** Following Ruby's spacing rule (space before the slash,
  none after, non-local callee), the slash starts a command-argument regex
  instead of dividing the call's result. Without a second slash on the line
  this fails loudly with "unterminated regex literal"; with one (for example
  `f /2 + g/i`) the line parses as `f(/2 + g/i)`, silently changing a former
  division chain, so audit division written in this spacing. To keep the
  division reading, space the slash on both sides (`f / 2`), remove the space
  (`f/2`), or call explicitly (`f() / 2`). Locals are unaffected, and `f /= 2`
  remains a compound assignment.
