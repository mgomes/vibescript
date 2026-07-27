- **Added: `String#*` repeats a string.** There was no way to build a separator,
  indent, table rule, or progress bar at a computed width without a loop -- a
  literal `"------"` was the only alternative. `"-" * 20` now works as in Ruby,
  truncating a float count toward zero and reporting a negative one. The
  projected size is charged against the memory quota before anything is
  allocated.
