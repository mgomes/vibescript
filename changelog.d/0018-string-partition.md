- **Added: Ruby-style `String#partition` and `String#rpartition`.** Both split a
  string into a three-element `[head, separator, tail]` triple around the first
  (`partition`) or last (`rpartition`) occurrence of the separator. A missing
  separator keeps the whole string on the head (`partition`) or tail
  (`rpartition`) with empty surrounding segments, and an empty separator matches
  at the start or end respectively, matching Ruby. The separator must be a
  string.
