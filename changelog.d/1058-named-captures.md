- **Added: named captures are accessible.** A pattern's named groups compiled and
  matched but were never surfaced, so `m[:name]` read nil and `named_captures`
  did not exist -- every non-trivial extraction had to be read back positionally.
  `m[:name]`, `m["name"]`, and `m.named_captures` now work. An index naming an
  entry the match result already has (`captures`, `pre_match`, ...) still reads
  that entry.
