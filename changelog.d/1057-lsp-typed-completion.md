- **Improved: completion after `.` narrows to the receiver's type.** It offered
  the union of all 306 builtin members whatever the receiver was, so a string
  receiver was offered money and temporal methods and completion could not rule
  out a wrong method name. It now narrows when the receiver's kind is known from
  the source -- a literal, or a parameter with a declared type -- and falls back
  to the full union for anything else.
