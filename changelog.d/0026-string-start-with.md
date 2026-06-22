- **Improved: Ruby-style `String#start_with?` and `String#end_with?`.** Both
  predicates now accept one or more string candidates and return true when any
  matches. Candidates are checked left to right and matching short-circuits like
  Ruby, so a non-string candidate is only rejected if reached before a match.
