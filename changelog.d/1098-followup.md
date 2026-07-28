- **Fixed: `vibes check` rejected symbol comparisons.** `:a < :b` runs, but the
  checker's comparison matrix accepted only numeric, string, money, duration,
  and time pairs, so check reported an unsupported comparison and `CheckedCall`
  refused to run the script.
