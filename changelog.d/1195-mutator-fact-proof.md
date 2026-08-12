- **Fixed: a deeply nested value no longer makes an in-place hash or array
  update report a mismatch against correct code.** An update keeps what the
  checker knows about the container only when the operands it evaluated left the
  variable holding the same value, and that comparison was made from a
  canonicalized key that stops at eight levels of nesting. A variable rebound
  while the update's own operands were being evaluated — to a value differing
  from the old one only below that depth — therefore looked untouched, so the
  update was applied to the value the variable used to hold and put it back.
  Reading the rebound field afterwards answered from the old value, and a call
  taking the new one was reported. The comparison now proves the two values are
  the same instead of grouping them, which is also cheaper: 1,600 `store` calls
  on a growing hash allocate 4.4MB against 11.2MB.
