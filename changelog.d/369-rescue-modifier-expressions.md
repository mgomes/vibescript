- **Added: Ruby-style rescue modifier expressions.** Expression-position
  `rescue` now works as a fallback form, so code such as
  `x = risky_call rescue fallback` evaluates the fallback when the guarded
  expression raises a recoverable runtime error. The modifier follows the same
  catchability rules as structured `begin`/`rescue`, so sandbox and limit errors
  remain unrescuable.
